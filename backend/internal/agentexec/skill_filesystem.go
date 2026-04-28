package agentexec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
)

type localSkillFilesystem struct {
	root string
}

func newLocalSkillFilesystem(root string) *localSkillFilesystem {
	absRoot, err := filepath.Abs(root)
	if err == nil {
		root = absRoot
	}
	return &localSkillFilesystem{root: filepath.Clean(root)}
}

func (b *localSkillFilesystem) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	base, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	result := make([]filesystem.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		result = append(result, fileInfo(entry.Name(), info))
	}
	return result, nil
}

func (b *localSkillFilesystem) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path, err := b.resolvePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &filesystem.FileContent{Content: sliceLines(string(data), req.Offset, req.Limit)}, nil
}

func (b *localSkillFilesystem) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, fmt.Errorf("pattern cannot be empty")
	}
	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	base, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	var matches []filesystem.GrepMatch
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if req.Glob != "" {
			ok, err := doublestar.Match(filepath.ToSlash(req.Glob), filepath.ToSlash(rel))
			if err != nil {
				return fmt.Errorf("invalid glob pattern: %w", err)
			}
			if !ok {
				return nil
			}
		}
		fileMatches, err := grepFile(path, re)
		if err != nil {
			return err
		}
		for _, match := range fileMatches {
			match.Path = filepath.ToSlash(rel)
			matches = append(matches, match)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (b *localSkillFilesystem) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	base, err := b.resolvePath(req.Path)
	if err != nil {
		return nil, err
	}
	pattern := filepath.ToSlash(req.Pattern)
	isAbsPattern := filepath.IsAbs(req.Pattern)
	var result []filesystem.FileInfo
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		matchPath := filepath.ToSlash(path)
		resultPath := path
		if !isAbsPattern {
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			matchPath = filepath.ToSlash(rel)
			resultPath = matchPath
		}
		ok, err := doublestar.Match(pattern, matchPath)
		if err != nil {
			return fmt.Errorf("invalid glob pattern: %w", err)
		}
		if ok {
			result = append(result, fileInfo(resultPath, info))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (b *localSkillFilesystem) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	path, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(req.Content), 0o644)
}

func (b *localSkillFilesystem) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	if req.OldString == "" {
		return fmt.Errorf("oldString must be non-empty")
	}
	path, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, req.OldString) {
		return fmt.Errorf("oldString not found in file: %s", req.FilePath)
	}
	if req.ReplaceAll {
		content = strings.ReplaceAll(content, req.OldString, req.NewString)
	} else {
		if strings.Count(content, req.OldString) != 1 {
			return fmt.Errorf("oldString must appear exactly once when replaceAll is false")
		}
		content = strings.Replace(content, req.OldString, req.NewString, 1)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (b *localSkillFilesystem) resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = b.root
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(b.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q is outside skill root %q", abs, b.root)
	}
	return abs, nil
}

func fileInfo(path string, info os.FileInfo) filesystem.FileInfo {
	return filesystem.FileInfo{
		Path:       path,
		IsDir:      info.IsDir(),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().Format(time.RFC3339Nano),
	}
}

func sliceLines(content string, offset int, limit int) string {
	if offset <= 1 && limit <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}

func grepFile(path string, re *regexp.Regexp) ([]filesystem.GrepMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []filesystem.GrepMatch
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if re.MatchString(text) {
			matches = append(matches, filesystem.GrepMatch{
				Line:    line,
				Content: text,
			})
		}
	}
	return matches, scanner.Err()
}

var _ filesystem.Backend = (*localSkillFilesystem)(nil)

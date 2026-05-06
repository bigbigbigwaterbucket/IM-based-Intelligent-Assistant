import { decode, encode } from "base-64";

const globals = globalThis as typeof globalThis & {
  atob?: (value: string) => string;
  btoa?: (value: string) => string;
};

if (typeof globals.atob !== "function") {
  globals.atob = decode;
}

if (typeof globals.btoa !== "function") {
  globals.btoa = encode;
}

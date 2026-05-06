const cryptoObject = {
  ensureSecure() {
    return Promise.resolve(true);
  },
  getRandomValues(values) {
    const crypto = globalThis.crypto;
    if (!crypto || typeof crypto.getRandomValues !== "function") {
      throw new Error("crypto.getRandomValues is unavailable. Ensure react-native-get-random-values is imported before Yjs.");
    }
    return crypto.getRandomValues(values);
  },
  subtle: globalThis.crypto ? globalThis.crypto.subtle : undefined,
};

module.exports = cryptoObject;

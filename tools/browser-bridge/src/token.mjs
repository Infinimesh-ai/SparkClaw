import { TOKEN_STORAGE_KEY } from "./protocol.mjs";

export function generateAuthToken(cryptoAPI = crypto) {
  const bytes = new Uint8Array(32);
  cryptoAPI.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

export function getOrCreateAuthToken(storage = localStorage, cryptoAPI = crypto) {
  let token = storage.getItem(TOKEN_STORAGE_KEY);
  if (!token) {
    token = generateAuthToken(cryptoAPI);
    storage.setItem(TOKEN_STORAGE_KEY, token);
  }
  return token;
}

export function rotateAuthToken(storage = localStorage, cryptoAPI = crypto) {
  const token = generateAuthToken(cryptoAPI);
  storage.setItem(TOKEN_STORAGE_KEY, token);
  return token;
}

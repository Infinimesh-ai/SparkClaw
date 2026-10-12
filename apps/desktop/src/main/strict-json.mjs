// Security-sensitive credential/profile JSON must not silently overwrite keys.
export function parseStrictJSON(raw) {
  const value = JSON.parse(raw);
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\]:]/gu) ?? [], objects = [];
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (token === "{") objects.push(new Set());
    else if (token === "[") objects.push(null);
    else if (token === "}" || token === "]") objects.pop();
    else if (token.startsWith('"') && tokens[index + 1] === ":") {
      const key = JSON.parse(token), object = objects.at(-1);
      if (!object || object.has(key)) throw new Error("Invalid JSON object key");
      object.add(key);
    }
  }
  return value;
}

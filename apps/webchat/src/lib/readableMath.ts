// The message renderer has no math typesetting. Model answers sometimes wrap
// arithmetic in LaTeX delimiters ($37 \times 4 = 148$); show it as ordinary
// text (37 × 4 = 148) instead of raw markup. Code spans stay literal.

const SYMBOLS: Record<string, string> = {
  times: "×", cdot: "·", div: "÷", pm: "±", mp: "∓", ast: "*",
  le: "≤", leq: "≤", ge: "≥", geq: "≥", ne: "≠", neq: "≠", approx: "≈", equiv: "≡", sim: "~",
  infty: "∞", to: "→", rightarrow: "→", leftarrow: "←", Rightarrow: "⇒", Leftrightarrow: "⇔",
  sum: "∑", prod: "∏", int: "∫", partial: "∂", circ: "°", degree: "°", angle: "∠",
  ldots: "…", cdots: "…", dots: "…", therefore: "∴", because: "∵", in: "∈", percent: "%",
  alpha: "α", beta: "β", gamma: "γ", delta: "δ", Delta: "Δ", epsilon: "ε", theta: "θ", lambda: "λ",
  mu: "μ", pi: "π", rho: "ρ", sigma: "σ", Sigma: "Σ", tau: "τ", phi: "φ", omega: "ω", Omega: "Ω",
  quad: " ", qquad: " ", left: "", right: "", displaystyle: "",
};
const SUPERSCRIPT: Record<string, string> = {
  "0": "⁰", "1": "¹", "2": "²", "3": "³", "4": "⁴", "5": "⁵", "6": "⁶", "7": "⁷", "8": "⁸", "9": "⁹",
  "+": "⁺", "-": "⁻", "(": "⁽", ")": "⁾", n: "ⁿ", "°": "°",
};
const SUBSCRIPT: Record<string, string> = {
  "0": "₀", "1": "₁", "2": "₂", "3": "₃", "4": "₄", "5": "₅", "6": "₆", "7": "₇", "8": "₈", "9": "₉",
  "+": "₊", "-": "₋", "(": "₍", ")": "₎",
};
const GROUP = String.raw`\{((?:[^{}]|\{[^{}]*\})*)\}`;
const LATEX_HINT = /\\[a-zA-Z]+|[\^_]|\\[{}%$]/;

function script(value: string, table: Record<string, string>, marker: string) {
  const chars = [...value];
  if (chars.length > 0 && chars.every(char => table[char])) return chars.map(char => table[char]).join("");
  return chars.length === 1 ? marker + value : `${marker}(${value})`;
}

function wrap(value: string) {
  return /^[\p{L}\p{N}.]+$/u.test(value) ? value : `(${value})`;
}

export function latexToText(source: string): string {
  let value = source.trim();
  for (let pass = 0; pass < 4; pass += 1) {
    const before = value;
    value = value
      .replace(new RegExp(String.raw`\\(?:text|mathrm|mathbf|mathit|operatorname|boxed|textbf)\s*` + GROUP, "g"), "$1")
      .replace(new RegExp(String.raw`\\[dt]?frac\s*` + GROUP + String.raw`\s*` + GROUP, "g"), (_, top: string, bottom: string) => `${wrap(latexToText(top))}/${wrap(latexToText(bottom))}`)
      .replace(new RegExp(String.raw`\\sqrt\s*` + GROUP, "g"), (_, inner: string) => `√${wrap(latexToText(inner))}`);
    if (value === before) break;
  }
  value = value
    .replace(/\^\s*\\circ/g, "°")
    .replace(/\\([{}%$#&_])/g, "$1")
    .replace(/\\[,;:!> ]/g, " ")
    .replace(/\\([a-zA-Z]+)/g, (match, name: string) => SYMBOLS[name] ?? match)
    .replace(new RegExp(String.raw`\^\s*(?:` + GROUP + String.raw`|([^\s{}]))`, "g"), (_, group: string | undefined, single: string | undefined) => script(latexToText(group ?? single ?? ""), SUPERSCRIPT, "^"))
    .replace(new RegExp(String.raw`_\s*(?:` + GROUP + String.raw`|([^\s{}]))`, "g"), (_, group: string | undefined, single: string | undefined) => script(latexToText(group ?? single ?? ""), SUBSCRIPT, "_"))
    .replace(/[{}]/g, "")
    .replace(/[ \t]+/g, " ");
  return value.trim();
}

function convertMath(text: string) {
  return text
    .replace(/\$\$([\s\S]+?)\$\$/g, (_, inner: string) => latexToText(inner))
    .replace(/\\\[([\s\S]+?)\\\]/g, (_, inner: string) => latexToText(inner))
    .replace(/\\\(([\s\S]+?)\\\)/g, (_, inner: string) => latexToText(inner))
    // Single-dollar math must look like LaTeX, so prices such as "$5 and $10"
    // remain unchanged.
    .replace(/\$(?!\s)([^$\n]+?)(?<!\s)\$/g, (match, inner: string) => LATEX_HINT.test(inner) ? latexToText(inner) : match);
}

export function readableMath(content: string): string {
  if (!content.includes("$") && !content.includes("\\(") && !content.includes("\\[")) return content;
  return content.split(/(```[\s\S]*?```|`[^`\n]*`)/).map((part, index) => index % 2 === 1 ? part : convertMath(part)).join("");
}

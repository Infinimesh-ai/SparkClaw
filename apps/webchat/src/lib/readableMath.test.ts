import { describe, expect, it } from "vitest";
import { latexToText, readableMath } from "./readableMath";

describe("readable math in answers", () => {
  it("turns LaTeX arithmetic into ordinary text", () => {
    expect(readableMath("所以总数为 $37 \\times 4 = 148$。")).toBe("所以总数为 37 × 4 = 148。");
    expect(readableMath("面积 \\(r^2 \\cdot \\pi\\)，结果 $$\\frac{148}{4} = 37$$")).toBe("面积 r² · π，结果 148/4 = 37");
    expect(readableMath("\\[\n\\text{总价} = 12 \\div 3 \\approx 4\n\\]")).toBe("总价 = 12 ÷ 3 ≈ 4");
    expect(latexToText("\\frac{a+b}{2} \\leq \\sqrt{x_1} + 30^\\circ")).toBe("(a+b)/2 ≤ √x₁ + 30°");
    expect(latexToText("x^{n+1}")).toBe("xⁿ⁺¹");
  });

  it("keeps prices and code literal", () => {
    expect(readableMath("票价 $5 和 $10，合计 $15")).toBe("票价 $5 和 $10，合计 $15");
    expect(readableMath("运行 `echo $a_1$` 后得到 $a_1$")).toBe("运行 `echo $a_1$` 后得到 a₁");
  });
});

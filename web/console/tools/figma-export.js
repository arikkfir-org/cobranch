// Exports the design system's tokens from the Figma file as tokens.json: every local variable, under the CSS name its
// codeSyntax gives it, with its value in each mode (a colour, a number, or another variable by its CSS name), and every
// text and effect style. It runs in the file, through the Figma Plugin API: paste it into a plugin's code (or give it
// to an agent's Figma tool), and save what it returns as web/console/tools/tokens.json. It only reads. `go generate`
// in web/console/tools then writes tokens.css from it (README.md beside this file).
const hex = (c) => "#" + [c.r, c.g, c.b, c.a === undefined || c.a === 1 ? null : c.a].filter((v) => v !== null)
  .map((v) => Math.round(v * 255).toString(16).padStart(2, "0")).join("");
const variables = await figma.variables.getLocalVariablesAsync();
const css = new Map(variables.map((v) => [v.id, (v.codeSyntax.WEB || "").replace(/^var\((.*)\)$/, "$1")]));
const missing = variables.filter((v) => !v.codeSyntax.WEB).map((v) => v.name);
if (missing.length) throw new Error("Variables without a CSS name in codeSyntax: " + missing.join(", "));
const value = (v) => v && v.type === "VARIABLE_ALIAS" ? { alias: css.get(v.id) } : typeof v === "object" ? hex(v) : v;
const collections = [];
for (const c of await figma.variables.getLocalVariableCollectionsAsync()) {
  const modes = c.modes.map((m) => m.name);
  collections.push({ name: c.name, modes, variables: c.variableIds.map((id) => variables.find((v) => v.id === id)).map((v) => ({
    name: v.name, css: css.get(v.id), type: v.resolvedType, description: v.description || undefined,
    values: Object.fromEntries(c.modes.map((m) => [m.name, value(v.valuesByMode[m.modeId])])),
  })) });
}
const unit = (u) => u.unit === "PIXELS" ? u.value : u.unit === "PERCENT" ? u.value + "%" : "auto";
const textStyles = (await figma.getLocalTextStylesAsync()).map((s) => ({
  name: s.name, family: s.fontName.family, style: s.fontName.style, size: s.fontSize,
  lineHeight: unit(s.lineHeight), letterSpacing: unit(s.letterSpacing), textCase: s.textCase === "ORIGINAL" ? undefined : s.textCase,
}));
const effectStyles = (await figma.getLocalEffectStylesAsync()).map((s) => ({
  name: s.name, effects: s.effects.filter((e) => e.visible !== false).map((e) => ({
    type: e.type, x: e.offset ? e.offset.x : undefined, y: e.offset ? e.offset.y : undefined, radius: e.radius, spread: e.spread,
    color: e.boundVariables && e.boundVariables.color ? { alias: css.get(e.boundVariables.color.id) } : e.color ? hex(e.color) : undefined,
  })),
}));
return { file: figma.fileKey, collections, textStyles, effectStyles };

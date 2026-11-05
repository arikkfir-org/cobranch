import { api } from "../../../lib/api.js";
import { agentPage, emptyState, mayEdit, newAgent, spendReport } from "../../../lib/agent.js";
import { ask, openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { banner, checkbox, failure, field, input, selectBox, textarea } from "../../../lib/forms.js";
import { avatar, icon } from "../../../lib/icons.js";
import { popover } from "../../../lib/menu.js";
import { go } from "../../../lib/router.js";

export const tab = { label: "Agents", order: 40 };
export const allowed = (can) => can("agents.create") || can("org.principals.manage");
export const view = "organization.settings";
export default ({ org, main, can }) => showAgents(org, main, can, new URLSearchParams(location.search).get("agent"));

// tabPath is Settings · Agents' path, with the editor of the agent id open when given.
const tabPath = (org, id) => "/" + org.slug + "/settings/agents" + (id ? "?" + new URLSearchParams({ agent: id }) : "");
// grantName names a grant as a list of them does: the role, and the repository it holds it on.
const grantName = (g) => g.repositorySlug ? g.repositorySlug + " · " + g.roleName : g.roleName;
// sectionHeader is a section's title over the line on what it holds, with its action on the right.
const sectionHeader = (title, description, action) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)), action || "");

// showAgents lists the organization's agents (Settings · Agents): the model each runs on, its cap, the secrets its tasks
// receive, its grants and its tasks this month, each with a menu; the agent editing names, when the viewer may change it,
// opens in its editor under them. saved says the last save worked.
async function showAgents(org, main, can, editing, saved = false) {
  const [agents, report] = await Promise.all([api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents), spendReport(org).catch(() => null)]);
  const monthly = new Map((report ? report.agents : []).map((s) => [s.agent.id, s.tasks]));
  const error = failure();
  const redraw = (id, ok) => showAgents(org, main, can, id, ok).catch(error.fail);
  const open = agents.find((a) => a.id === editing && mayEdit(a, can));
  const row = (a) => h("tr", { class: a === open ? "row-selected" : "" },
    h("td", {}, h("a", { class: "agents-agent agents-agent-small", href: agentPage(org, a.id), "data-nav": "" }, avatar(a.name, true, 24),
      h("span", { class: "cell-two-lines" }, h("span", {}, a.name), a.description ? h("span", {}, a.description) : ""))),
    h("td", { class: a.model ? "cell-mono" : "settings-agents-unset" }, a.model || "Not set"),
    h("td", { class: a.cap ? "cell-mono" : "cell-muted" }, a.cap ? "$" + a.cap : "—"),
    h("td", { class: Object.keys(a.secrets).length ? "cell-mono" : "cell-muted" }, Object.keys(a.secrets).join(", ") || "—"),
    h("td", { class: a.grants.length ? "" : "cell-muted" }, a.grants.map(grantName).join(", ") || "None"),
    h("td", {}, report ? String(monthly.get(a.id) || 0) : "–"),
    h("td", { class: "cell-actions" }, rowMenu(org, a, can)));
  const table = agents.length ? h("table", { class: "table settings-agents" }, h("thead", {}, h("tr", {}, ["Agent", "Model", "Cap per task", "Secrets", "Grants", "Tasks"].map((t) => h("th", {}, t)),
    h("th", { "aria-label": "Actions" }))), h("tbody", {}, agents.map(row)))
    : h("div", { class: "card" }, emptyState("bot", "No agents yet", "An agent takes the tasks it is given. Make one with New agent."));
  main.replaceChildren(error.el, saved ? banner("success", null, "Saved.") : "",
    sectionHeader("Agents", "Each agent runs on one model with its own instructions, the secrets it may use, an optional cap, and the grants that say what it may touch.",
      can("agents.create") ? h("button", { class: "btn btn-primary", type: "button", onclick: () => newAgent(org).catch(error.fail) }, icon("plus"), "New agent") : ""),
    table, open ? await editor(org, open, can, redraw) : "");
}

// rowMenu is an agent's menu: edit it and open its page, and delete it for a viewer who manages the organization's
// principals, unless Cobranch made it.
function rowMenu(org, a, can) {
  const items = [
    mayEdit(a, can) ? ["Edit", "pencil", () => go(tabPath(org, a.id))] : null,
    ["Open its page", "bot", () => go("/" + org.slug + "/agents/" + a.id)],
    can("org.principals.manage") && !a.builtIn ? ["Delete " + a.name, "trash-2", () => remove(org, a), true] : null,
  ].filter(Boolean);
  const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More for " + a.name, title: "More for " + a.name }, icon("ellipsis"));
  button.addEventListener("click", () => popover(button, "end", (close) => items.map(([word, glyph, run, danger]) =>
    h("button", { class: "menu-item" + (danger ? " menu-item-danger" : ""), type: "button", role: "menuitem", onclick: () => { close(); run(); } }, icon(glyph), h("span", {}, word)))));
  return button;
}

// remove asks before it deletes agent a, with its grants and its tasks, then lists the agents left.
function remove(org, a) {
  return ask("Delete " + a.name + "?", a.name + " goes with its grants and its tasks, and its tasks' transcripts. This can't be undone.", "Delete",
    () => api("DELETE", "/orgs/" + org.slug + "/agents/" + a.id, undefined, { "If-Match": `"${a.version}"` }).then(() => go(tabPath(org), true)));
}

// editor is agent a's editor (Card): its name, model and cap, what it is for, its standing instructions, the secrets its
// tasks receive and its grants, with Cancel and Save. Its creator changes its words; a viewer who manages the
// organization's principals changes the rest too, and one who manages its grants lets it read its provider's key.
async function editor(org, a, can, redraw) {
  const manage = can("org.principals.manage");
  const [models, owned] = await Promise.all([api("GET", "/orgs/" + org.slug + "/models").then((d) => d.models, () => []), manage ? ownedSecrets(org) : []]);
  const error = failure();
  const key = (m) => m.provider + "\n" + m.name;
  const current = a.provider ? a.provider + "\n" + a.model : "";
  const name = input({ required: true, maxlength: 100, value: a.name });
  const model = h("select", { disabled: !manage }, h("option", { value: "" }, "Not set"),
    models.map((m) => h("option", { value: key(m), selected: key(m) === current }, m.name + (m.default ? " (default)" : ""))),
    current && !models.some((m) => key(m) === current) ? h("option", { value: current, selected: true }, a.model) : "");
  const readKey = h("input", { type: "checkbox", checked: true });
  const cap = input({ value: a.cap || "", placeholder: "None", inputmode: "decimal", pattern: "\\d{1,7}(\\.\\d{1,6})?", disabled: !manage });
  const description = input({ maxlength: 1000, value: a.description });
  const instructions = textarea({ maxlength: 20000, rows: 5 });
  instructions.value = a.instructions;
  const secrets = secretLines(a, owned, manage);
  const save = h("button", { class: "btn btn-primary", type: "submit" }, "Save");
  const changes = { name, description, instructions, cap, model, readKey, secrets };
  return h("form", { class: "card settings-agent", onsubmit: (e) => { e.preventDefault(); saveAgent(org, a, can, changes, current, save, error).then(() => redraw(a.id, true), () => {}); } },
    h("header", { class: "card-header" }, h("h2", { class: "card-title" }, a.name), h("span", { class: "card-meta" }, "editing"),
      manage && !a.builtIn ? h("button", { class: "btn btn-danger btn-sm", type: "button", onclick: () => remove(org, a) }, "Delete agent") : ""),
    h("div", { class: "card-body" }, error.el,
      h("div", { class: "settings-agent-fields" }, field("Name", name),
        field("Model", selectBox(model), manage ? [h("span", {}, "From Settings · Models."), can("org.grants.manage") ? checkbox(readKey, "Let it read the new model's provider key") : ""] : "Changed by whoever manages the organization's principals."),
        field("Cap per task", cap, "Optional. A task fails once it spends this much, in US dollars.")),
      field("Description", description, "One line, shown on the Agents page and in menus."), field("Instructions", instructions, "What the agent is told before every task."),
      secrets.el, grantsField(org, a, can, () => redraw(a.id))),
    h("footer", { class: "card-footer" }, h("button", { class: "btn", type: "button", onclick: () => go(tabPath(org)) }, "Cancel"), save));
}

// saveAgent saves what changed in agent a's editor, each step on the version the last one answered: its words and cap,
// then its model, letting it read the model's provider key when asked, then its secrets. It shows what failed and
// rejects, leaving the editor as the viewer filled it.
async function saveAgent(org, a, can, c, current, button, error) {
  const path = "/orgs/" + org.slug + "/agents/" + a.id;
  const manage = can("org.principals.manage");
  button.disabled = true;
  error.el.hidden = true;
  try {
    const patch = { name: c.name.value, description: c.description.value, instructions: c.instructions.value };
    if (manage && c.cap.value.trim() !== (a.cap || "")) patch.cap = c.cap.value.trim() || null;
    let now = await api("PATCH", path, patch, { "If-Match": `"${a.version}"` });
    if (manage && c.model.value !== current) {
      const [provider, model] = c.model.value ? c.model.value.split("\n") : [];
      now = await api("PUT", path + "/model", provider ? { provider, model } : {}, { "If-Match": `"${now.version}"` });
      if (provider && c.readKey.checked && can("org.grants.manage")) await readProviderKey(org, now, provider);
    }
    if (manage && c.secrets.changed()) now = await api("PUT", path + "/secrets", { secrets: c.secrets.value() }, { "If-Match": `"${now.version}"` });
  } catch (err) {
    error.fail(err);
    button.disabled = false;
    throw err;
  }
}

// readProviderKey grants agent a Secret reader on the secret that holds provider's key, which its tasks need to run;
// one it already reads stays as it is.
async function readProviderKey(org, a, provider) {
  const p = await api("GET", "/orgs/" + org.slug + "/model-providers/" + encodeURIComponent(provider));
  if (!p.secretId) return;
  await api("POST", "/orgs/" + org.slug + "/secrets/" + p.secretId + "/access", { principalId: a.id })
    .catch((err) => { if (err.code !== "grant.exists") throw new Error("The model is picked, but the agent may not read the provider's key: " + err.message); });
}

// ownedSecrets are the secrets the viewer manages, the organization's and its repositories', which an agent's tasks may
// receive.
async function ownedSecrets(org) {
  const repos = await api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories, () => []);
  const lists = await Promise.all([api("GET", "/orgs/" + org.slug + "/secrets").then((d) => d.secrets.map((s) => ({ id: s.id, label: s.name })), () => []),
    ...repos.map((r) => api("GET", "/orgs/" + org.slug + "/repos/" + r.slug + "/secrets").then((d) => d.secrets.map((s) => ({ id: s.id, label: r.slug + " / " + s.name })), () => []))]);
  return lists.flat();
}

// secretLines is the editor of the secrets agent a's tasks receive: each an environment variable's name and one of the
// secrets the viewer manages, added and removed in place; changed says whether they differ from the agent's and value
// is them by name.
function secretLines(a, owned, manage) {
  const lines = h("div", { class: "settings-agent-secrets" });
  const line = (env = "", id = "") => {
    const name = input({ required: true, maxlength: 100, pattern: "[A-Za-z_][A-Za-z0-9_]*", value: env, placeholder: "NPM_TOKEN", "aria-label": "Environment variable", disabled: !manage });
    const pick = h("select", { required: true, "aria-label": "Secret", disabled: !manage }, owned.map((s) => h("option", { value: s.id, selected: s.id === id }, s.label)),
      id && !owned.some((s) => s.id === id) ? h("option", { value: id, selected: true }, "A secret you don't manage") : "");
    const el = h("div", { class: "settings-agent-secret" }, name, selectBox(pick),
      manage ? h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Remove", title: "Remove", onclick: () => el.remove() }, icon("x")) : "");
    el.named = () => [name.value.trim(), pick.value];
    return el;
  };
  lines.append(...Object.entries(a.secrets).map(([env, id]) => line(env, id)));
  const value = () => Object.fromEntries([...lines.children].map((el) => el.named()));
  const el = h("div", { class: "field" }, h("span", { class: "field-label" }, "Secrets"), lines,
    manage && owned.length ? h("div", {}, h("button", { class: "btn btn-sm", type: "button", onclick: () => lines.append(line()) }, icon("plus"), "Add secret")) : "",
    h("span", { class: "field-help" }, "From Settings · Secrets. Each goes in a task's environment under its name, if the agent may read it: grant it Secret reader on the secret."));
  return { el, value, changed: () => JSON.stringify(Object.entries(value()).sort()) !== JSON.stringify(Object.entries(a.secrets).sort()) };
}

// grantsField is agent a's grants: its roles at the organization, which it got when it was made, and on repositories,
// which a viewer who manages the organization's grants adds and removes; done draws the editor again.
function grantsField(org, a, can, done) {
  const manage = can("org.grants.manage");
  const revoke = (g) => api("DELETE", "/orgs/" + org.slug + "/repos/" + g.repositorySlug + "/access/" + a.id + "/roles/" + g.roleId).then(done);
  const row = (g) => h("tr", {}, h("td", {}, g.repositorySlug || "The organization"), h("td", {}, g.roleName),
    h("td", { class: "cell-actions" }, manage && g.repositorySlug ? h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Revoke " + grantName(g), title: "Revoke",
      onclick: () => ask("Revoke " + g.roleName + " on " + g.repositorySlug + "?", a.name + "'s tasks lose what the role gives them there.", "Revoke", () => revoke(g)) }, icon("trash-2")) : ""));
  return h("div", { class: "field" }, h("span", { class: "field-label" }, "Grants"),
    a.grants.length ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Scope"), h("th", {}, "Role"), h("th", { "aria-label": "Actions" }))), h("tbody", {}, a.grants.map(row)))
      : h("p", { class: "field-help" }, "None: its tasks may touch nothing."),
    manage ? h("div", {}, h("button", { class: "btn btn-sm", type: "button", onclick: () => addGrant(org, a, done) }, icon("plus"), "Add grant")) : "");
}

// addGrant asks for a role on one of the organization's repositories and gives it to agent a there.
async function addGrant(org, a, done) {
  const repos = await api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories, () => []);
  const repo = h("select", { required: true }, repos.map((r) => h("option", { value: r.slug }, r.name)));
  const role = h("select", { required: true });
  const fill = () => api("GET", "/orgs/" + org.slug + "/repos/" + repo.value + "/roles").then((d) => role.replaceChildren(...d.roles.map((r) => h("option", { value: r.id }, r.name))), () => role.replaceChildren());
  repo.addEventListener("change", fill);
  if (repos.length) fill();
  openDialog({ title: "Give " + a.name + " a role", action: "Grant",
    body: [field("Repository", selectBox(repo)), field("Role", selectBox(role), "Only roles whose every permission you hold there.")],
    submit: () => api("POST", "/orgs/" + org.slug + "/repos/" + repo.value + "/access/" + a.id + "/roles", { roleId: role.value }).then(done) });
}

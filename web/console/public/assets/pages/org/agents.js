import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { when } from "../../lib/format.js";
import { go } from "../../lib/router.js";
import { base, me, roles, setAgents } from "../../lib/state.js";

// nav puts Agents in the sidebar, with the organization's agents under it, a dot on each that works on a task now.
export const nav = { label: "Agents", order: 50, icon: "bot", tab: "Agents", items: agentItems };
export const crumbs = [{ label: "Agents" }];
export default ({ org, main, can }) => showAgents(org, main, can);

// agentItems are the organization's agents as the sidebar lists them, each marked while one of its tasks runs; it
// keeps their IDs too, for the conversation pane to tell an agent's message from a person's.
async function agentItems({ org }) {
  const [agents, running] = await Promise.all([api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/tasks?state=running&limit=100").then((d) => new Set(d.tasks.map((t) => t.agent.id)), () => new Set())]);
  setAgents(new Set(agents.map((a) => a.id)));
  return { whole: true, count: agents.length, items: agents.map((a) => ({ ...agentItem(org, a),
    dot: running.has(a.id) ? "ai" : null, dotLabel: running.has(a.id) ? "Working" : null })) };
}

// agentItem is agent a as a sub item of the sidebar's Agents: it opens the agent's tasks, which put it first there.
export const agentItem = (org, a) => ({ key: a.id, label: a.name, href: base + org.slug + "/tasks?" + new URLSearchParams({ agent: a.id }), avatar: "agent" });

// grantChips names an agent's grants: a role at the organization, or a role on one of its repositories.
const grantChips = (grants) => grants.length ? grants.map((g) => h("span", { class: "chip" }, g.roleName, g.repositorySlug ? " on " + g.repositorySlug : ""))
  : h("span", { class: "muted" }, "None");
// repoSelect picks one of the organization's repositories by ID, or none.
const repoSelect = (repos, none) => h("select", {}, h("option", { value: "" }, none), repos.map((r) => h("option", { value: r.id }, r.name)));

// showAgents renders the organization's agents, a form for a viewer holding agents.create to make one, and one for a
// viewer holding agents.assign to assign a task. An agent's creator changes it; org.principals.manage changes and
// deletes any, and picks its model and the secrets its tasks receive. problem, when given, is what went wrong in the
// step that drew the page again.
async function showAgents(org, main, can, problem = null) {
  const error = h("p", { class: "alert", hidden: !problem }, problem || "");
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = () => showAgents(org, main, can).catch(fail);
  // redraw draws the page again, saying what went wrong, if anything, in the step that drew it.
  const redraw = (problem = null) => showAgents(org, main, can, problem).catch(fail);
  const manage = can("org.principals.manage");
  const [list, repos, providers] = await Promise.all([api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
    api("GET", "/orgs/" + org.slug + "/repos").then((d) => d.repositories),
    api("GET", "/orgs/" + org.slug + "/model-providers").then((d) => d.modelProviders)]);
  const agentPath = (a) => "/orgs/" + org.slug + "/agents/" + a.id;
  const edit = (a, row) => {
    const name = h("input", { required: true, maxlength: 100, value: a.name });
    const description = h("input", { maxlength: 1000, value: a.description });
    const instructions = h("textarea", { maxlength: 20000, rows: 6 }, a.instructions);
    const editor = h("tr", {}, h("td", { colspan: 5 }, h("form", { onsubmit: (e) => {
      e.preventDefault();
      api("PATCH", agentPath(a), { name: name.value, description: description.value, instructions: instructions.value }, { "If-Match": `"${a.version}"` })
        .then(refresh).catch(fail);
    } }, h("label", {}, "Name", name), h("label", {}, "Description", description), h("label", {}, "Standing instructions", instructions),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save"), " ", h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(row) }, "Cancel")))));
    row.replaceWith(editor);
  };
  const agentRow = (a) => {
    const mine = a.createdBy && a.createdBy.id === me.id;
    const named = Object.keys(a.secrets).length;
    const row = h("tr", {}, h("td", {}, a.name, a.builtIn ? [" ", h("span", { class: "chip" }, "Built-in")] : null, a.description ? h("p", {}, a.description) : null),
      h("td", {}, grantChips(a.grants)),
      h("td", {}, a.provider ? [a.provider, " · ", h("code", {}, a.model)] : h("span", { class: "muted" }, "None"),
        named ? h("p", {}, named === 1 ? "1 secret" : named + " secrets") : null),
      h("td", {}, a.createdBy ? a.createdBy.displayName : h("span", { class: "muted" }, a.builtIn ? "Cobranch" : "Someone since gone"), h("p", {}, when(a.createdAt))),
      h("td", { class: "actions" },
        h("a", { class: "button small", href: base + org.slug + "/tasks?" + new URLSearchParams({ agent: a.id }), "data-nav": "" }, "Tasks"),
        mine || manage ? [" ", h("button", { class: "button small", type: "button", onclick: () => edit(a, row) }, "Edit")] : null,
        manage ? [" ", h("button", { class: "button small", type: "button", onclick: () => row.replaceWith(modelEditor(org, a, providers, can, row, redraw)) }, "Model"),
          " ", h("button", { class: "button small", type: "button", onclick: () => secretsEditor(org, a, repos, row, redraw).then((editor) => row.replaceWith(editor)).catch(fail) }, "Secrets")] : null,
        manage && !a.builtIn ? [" ", h("button", { class: "button small danger", type: "button", title: "Delete " + a.name + ", its grants and its tasks",
          onclick: () => confirm("Delete " + a.name + " with its grants and tasks?") && api("DELETE", agentPath(a), undefined, { "If-Match": `"${a.version}"` }).then(refresh).catch(fail) }, "Delete")] : null));
    return row;
  };
  const sections = [error, h("section", { class: "panel" },
    h("p", {}, "An agent is a principal of this organization that takes tasks, with roles of its own. A task does no more than both its agent and its assigner may, so assigning one never lends anyone more than they hold."),
    list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Agent"), h("th", {}, "Roles"), h("th", {}, "Model"), h("th", {}, "Created by"), h("th", {}))), h("tbody", {}, list.map(agentRow)))
      : h("p", {}, "No agents yet."))];
  if (can("agents.create")) sections.push(newAgentForm(org, repos, refresh, fail));
  if (can("agents.assign") && list.length) sections.push(assignForm(org, list, repos, fail));
  main.replaceChildren(...sections);
}

// modelEditor puts agent a on a model of one of the organization's providers, or on none, in place of row; it adds a
// model the organization hasn't to its models, unpriced, for a viewer who manages the organization, since an agent
// runs only on one of them, and offers a viewer who manages the organization's grants to let the agent read the
// provider's key in the same step, which a task needs to run. done draws the page again, with what went wrong, if
// anything.
function modelEditor(org, a, providers, can, row, done) {
  const provider = h("select", {}, h("option", { value: "" }, "None"),
    providers.map((p) => h("option", { value: p.name, selected: p.name === a.provider }, p.name + (p.broken ? " (its key is gone)" : ""))));
  const model = h("input", { maxlength: 200, value: a.model || "", placeholder: "As the provider names it" });
  const grant = h("input", { type: "checkbox", checked: true });
  const editor = h("tr", {}, h("td", { colspan: 5 }, h("form", { onsubmit: async (e) => {
    e.preventDefault();
    const picked = providers.find((p) => p.name === provider.value);
    try {
      if (picked && can("org.manage")) {
        await api("PUT", "/orgs/" + org.slug + "/models/" + encodeURIComponent(picked.name) + "/" + encodeURIComponent(model.value.trim()),
          { inputPrice: "0", outputPrice: "0" }, { "If-None-Match": "*" }).catch((err) => { if (err.code !== "precondition.failed") throw err; });
      }
      await api("PUT", "/orgs/" + org.slug + "/agents/" + a.id + "/model", picked ? { provider: picked.name, model: model.value } : {}, { "If-Match": `"${a.version}"` });
    } catch (err) {
      return done(err.message);
    }
    if (!picked || !picked.secretId || !grant.checked || !can("org.grants.manage")) return done();
    api("POST", "/orgs/" + org.slug + "/secrets/" + picked.secretId + "/access", { principalId: a.id })
      .then(() => done(), (err) => done(err.code === "grant.exists" ? null : "The model is picked, but the agent may not read the provider's key: " + err.message));
  } }, h("h3", {}, a.name + "'s model"),
    providers.length ? null : h("p", {}, "The organization has no model providers yet: an admin adds one, with its key, in Settings."),
    h("label", {}, "Provider", provider),
    h("label", {}, "Model", h("span", {}, "Its name as the provider knows it, one of the organization's models; an admin's new one is added unpriced. Cobranch doesn't check it, so a model works the day the provider ships it."), model),
    can("org.grants.manage") ? h("label", { class: "check" }, grant, "Let the agent read the provider's key (Secret reader on it), as its tasks need") : null,
    h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save"), " ", h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(row) }, "Cancel")))));
  return editor;
}

// secretsEditor names the secrets agent a's tasks receive, each an environment variable from one of the secrets the
// viewer manages, the organization's or its repositories', in place of row. done draws the page again.
async function secretsEditor(org, a, repos, row, done) {
  const lists = await Promise.all([api("GET", "/orgs/" + org.slug + "/secrets").then((d) => d.secrets.map((s) => ({ id: s.id, label: s.name }))).catch(() => []),
    ...repos.map((r) => api("GET", "/orgs/" + org.slug + "/repos/" + r.slug + "/secrets").then((d) => d.secrets.map((s) => ({ id: s.id, label: r.slug + " / " + s.name }))).catch(() => []))]);
  const owned = lists.flat();
  const lines = h("div");
  const line = (name = "", id = "") => {
    const env = h("input", { required: true, maxlength: 100, pattern: "[A-Za-z_][A-Za-z0-9_]*", value: name, placeholder: "NPM_TOKEN" });
    const pick = h("select", { required: true }, owned.map((s) => h("option", { value: s.id, selected: s.id === id }, s.label)),
      id && !owned.some((s) => s.id === id) ? h("option", { value: id, selected: true }, "A secret you don't manage") : null);
    const el = h("div", { class: "row" }, env, pick, h("button", { class: "button small", type: "button", onclick: () => el.remove() }, "Remove"));
    el.named = () => [env.value.trim(), pick.value];
    return el;
  };
  lines.append(...Object.entries(a.secrets).map(([name, id]) => line(name, id)));
  const editor = h("tr", {}, h("td", { colspan: 5 }, h("form", { onsubmit: (e) => {
    e.preventDefault();
    const secrets = Object.fromEntries([...lines.children].map((el) => el.named()));
    api("PUT", "/orgs/" + org.slug + "/agents/" + a.id + "/secrets", { secrets }, { "If-Match": `"${a.version}"` }).then(() => done(), (err) => done(err.message));
  } }, h("h3", {}, "Secrets " + a.name + "'s tasks receive"),
    h("p", {}, "Each goes in a task's environment under the name given, if the agent may read it: grant it Secret reader on the secret. Not PATH, HOME, or a name starting COBRANCH_, GIT_ or LD_."),
    lines,
    h("div", {}, owned.length ? h("button", { class: "button small", type: "button", onclick: () => lines.append(line()) }, "Add a secret") : h("span", { class: "muted" }, "No secrets you manage to add.")),
    h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save"), " ", h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(row) }, "Cancel")))));
  return editor;
}

// newAgentForm makes an agent with its first roles: at the organization, and perhaps one on a repository, each one
// whose every permission the viewer holds there.
function newAgentForm(org, repos, done, fail) {
  const name = h("input", { required: true, maxlength: 100, placeholder: "Release notes writer" });
  const description = h("input", { maxlength: 1000 });
  const instructions = h("textarea", { maxlength: 20000, rows: 4, placeholder: "What every task of it starts from, in Markdown." });
  const picks = roles.map((r) => h("label", {}, h("input", { type: "checkbox", value: r.id }), r.name));
  const repo = repoSelect(repos, "No repository");
  const repoRole = h("select", {}, h("option", { value: "" }, "No role"));
  // The roles a repository grants are those of the one picked.
  repo.addEventListener("change", () => {
    repoRole.replaceChildren(h("option", { value: "" }, "No role"));
    const picked = repos.find((r) => r.id === repo.value);
    if (picked) api("GET", "/orgs/" + org.slug + "/repos/" + picked.slug + "/roles").then((d) => repoRole.append(...d.roles.map((r) => h("option", { value: r.id }, r.name)))).catch(fail);
  });
  return h("section", { class: "panel" }, h("h3", {}, "Make an agent"), h("p", {}, "Only roles whose every permission you hold where they apply."),
    h("form", { onsubmit: (e) => {
      e.preventDefault();
      const grants = picks.map((l) => l.firstChild).filter((c) => c.checked).map((c) => ({ roleId: c.value }));
      if (repo.value && repoRole.value) grants.push({ roleId: repoRole.value, repositoryId: repo.value });
      api("POST", "/orgs/" + org.slug + "/agents", { name: name.value, description: description.value, instructions: instructions.value, grants }).then(done).catch(fail);
    } }, h("label", {}, "Name", name), h("label", {}, "Description", description), h("label", {}, "Standing instructions", instructions),
      h("label", {}, "Roles", h("div", { class: "picks" }, picks)), h("label", {}, "A role on a repository", h("div", { class: "row" }, repo, repoRole)),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Make the agent"))));
}

// assignForm assigns a task to one of the agents, perhaps in a repository the viewer reads and on one of its
// branches, and opens the organization's tasks.
function assignForm(org, list, repos, fail) {
  const agent = h("select", { required: true }, list.map((a) => h("option", { value: a.id }, a.name)));
  const title = h("input", { required: true, maxlength: 200, placeholder: "Write the release notes for 1.4" });
  const instructions = h("textarea", { maxlength: 20000, rows: 4 });
  const repo = repoSelect(repos, "No repository");
  const branch = h("input", { placeholder: "main" });
  return h("section", { class: "panel" }, h("h3", {}, "Assign a task"),
    h("p", {}, "The task does no more than both the agent and you may, as you are whenever it acts."),
    h("form", { onsubmit: (e) => {
      e.preventDefault();
      const body = { agentId: agent.value, title: title.value, instructions: instructions.value };
      if (repo.value) body.repositoryId = repo.value;
      if (repo.value && branch.value.trim()) body.branch = branch.value.trim();
      api("POST", "/orgs/" + org.slug + "/tasks", body).then(() => go("/" + org.slug + "/tasks")).catch(fail);
    } }, h("label", {}, "Agent", agent), h("label", {}, "Title", title), h("label", {}, "Instructions", instructions),
      h("label", {}, "Repository", repo), h("label", {}, "Branch", branch),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Assign"))));
}

import { api } from "../../../lib/api.js";
import { agentPage, emptyState, providerName } from "../../../lib/agent.js";
import { ask, openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { checkbox, failure, field, input, selectBox } from "../../../lib/forms.js";
import { plural } from "../../../lib/format.js";
import { avatar, icon } from "../../../lib/icons.js";
import { popover } from "../../../lib/menu.js";
import { base } from "../../../lib/state.js";

export const tab = { label: "Models", order: 60 };
export const allowed = (can) => can("org.manage");
export const view = "organization.settings";
export default ({ org, main, can }) => showModels(org, main, can);

// sectionHeader is a section's title over the line on what it holds, with its action on the right.
const sectionHeader = (title, description, action) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)), action || "");
// providerPath and modelPath are the API's paths of one of the organization's providers and models.
const providerPath = (org, name) => "/orgs/" + org.slug + "/model-providers/" + encodeURIComponent(name);
const modelPath = (org, m) => "/orgs/" + org.slug + "/models/" + encodeURIComponent(m.provider) + "/" + encodeURIComponent(m.name);
// more is a row's menu of what the viewer may do with it.
function more(label, items) {
  const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": label, title: label }, icon("ellipsis"));
  button.addEventListener("click", () => popover(button, "end", (close) => items.map(([word, glyph, run, danger]) =>
    h("button", { class: "menu-item" + (danger ? " menu-item-danger" : ""), type: "button", role: "menuitem", onclick: () => { close(); run(); } }, icon(glyph), h("span", {}, word)))));
  return button;
}

// showModels is Settings · Models: the providers the organization's agents reach, each through one of its secrets, those
// the installation knows that it hasn't connected, and its models, which its agents pick from: their provider, the
// agents on them and the default a new agent starts on, each priced per million tokens, from which Cobranch prices
// every call. What a task may spend is its agent's cap, in Settings · Agents.
async function showModels(org, main, can) {
  const error = failure();
  const refresh = () => showModels(org, main, can).catch(error.fail);
  const [{ modelProviders: providers, available }, models, keys] = await Promise.all([api("GET", "/orgs/" + org.slug + "/model-providers"),
    api("GET", "/orgs/" + org.slug + "/models").then((d) => d.models),
    can("secrets.manage") ? api("GET", "/orgs/" + org.slug + "/secrets").then((d) => d.secrets) : []]);
  const left = available.filter((name) => !providers.some((p) => p.name === name));
  const connect = (name) => connectProvider(org, keys, left, name, null, refresh);
  main.replaceChildren(error.el,
    sectionHeader("Providers", "A provider is reached with one of the organization's secrets. Its models become available to every agent; pick the default each new agent starts with.",
      left.length ? h("button", { class: "btn", type: "button", onclick: () => connect(left[0]) }, icon("plus"), "Add provider") : ""),
    providers.length || left.length ? h("div", { class: "card" }, ...providers.map((p) => providerRow(org, p, models, keys, refresh)),
      ...left.map((name) => h("div", { class: "card-row" }, h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, providerName(name)),
        h("span", { class: "card-row-meta" }, "Secret — · not configured")), h("span", { class: "badge-quiet tone-neutral" }, "Not configured"),
      h("button", { class: "btn btn-sm", type: "button", onclick: () => connect(name) }, "Connect"))))
      : h("div", { class: "card" }, emptyState("key-round", "No providers", "This installation knows no model providers, so its agents run on none.")),
    sectionHeader("Models", "Agents pick from these. Spend is tokens times the model's prices; each agent's settings carry its own cap per task.",
      providers.length ? h("button", { class: "btn", type: "button", onclick: () => modelDialog(org, providers, null, refresh) }, icon("plus"), "Add model") : ""),
    models.length ? modelsTable(org, providers, models, refresh, error.fail)
      : h("div", { class: "card" }, providers.length ? emptyState("sparkles", "No models yet", "Add the models the organization's agents may run on, with what they cost.")
        : emptyState("sparkles", "No models until a provider is connected", "Each provider brings the models it serves; add them here with their prices.")));
}

// providerRow is a provider the organization connected: the secret its key is in and how many models it has, whether
// its key is still there, and Manage, which names another secret or removes it.
function providerRow(org, p, models, keys, refresh) {
  const count = models.filter((m) => m.provider === p.name).length;
  const secret = p.secretName ? h("code", {}, p.secretName) : p.broken ? "gone" : "one you don't manage";
  return h("div", { class: "card-row" },
    h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, providerName(p.name)),
      h("span", { class: "card-row-meta" }, "Secret ", secret, " · " + plural(count, "model"))),
    p.broken ? h("span", { class: "badge-quiet tone-danger" }, "Its key is gone") : h("span", { class: "badge-quiet tone-success" }, "Connected"),
    h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => connectProvider(org, keys, [], p.name, p, refresh) }, "Manage"));
}

// connectProvider asks for the secret that holds a provider's key: connecting one of left, the providers not yet
// connected, starting at name, or naming another for p, which it may also remove once no agent runs on it.
function connectProvider(org, keys, left, name, p, refresh) {
  const provider = h("select", { required: true, disabled: !!p }, (p ? [name] : left).map((n) => h("option", { value: n, selected: n === name }, providerName(n))));
  const key = h("select", { required: true }, keys.map((s) => h("option", { value: s.id, selected: p && s.id === p.secretId }, s.name)));
  const body = [field("Provider", selectBox(provider)),
    keys.length ? field("Secret", selectBox(key), "One of the organization's secrets, which holds the provider's API key. An agent's tasks run only while the agent may read it.")
      : h("p", {}, "The key goes in one of the organization's secrets first: add it in ", h("a", { href: base + org.slug + "/settings/secrets", "data-nav": "" }, "Settings · Secrets"), ".")];
  if (p) body.push(h("p", { class: "field-help" }, h("button", { class: "btn btn-ghost btn-sm settings-models-remove", type: "button", onclick: () => removeProvider(org, p, refresh) },
    icon("trash-2"), "Remove " + providerName(p.name))));
  openDialog({ title: p ? providerName(p.name) : "Connect a provider", action: p ? "Save" : "Connect", body,
    submit: () => keys.length ? api("PUT", providerPath(org, provider.value), { secretId: key.value }, p ? { "If-Match": `"${p.version}"` } : { "If-None-Match": "*" }).then(refresh)
      : Promise.reject(new Error("Add the provider's key as one of the organization's secrets first.")) });
}

// removeProvider asks before it removes provider p, which the API refuses while agents run on it.
function removeProvider(org, p, refresh) {
  document.querySelector("dialog[open]")?.close();
  ask("Remove " + providerName(p.name) + "?", "Its models go with it. Agents on them must move to another model first.", "Remove",
    () => api("DELETE", providerPath(org, p.name), undefined, { "If-Match": `"${p.version}"` }).then(refresh));
}

// modelsTable is the organization's models: each one's provider, the agents on it, whether it is the default, and a
// menu that changes its prices, makes it the default, or removes it once no agent runs on it; fail shows what failed.
function modelsTable(org, providers, models, refresh, fail) {
  const usedBy = (m) => m.agents.length ? h("span", { class: "settings-models-agents" }, m.agents.map((a) =>
    h("a", { class: "settings-models-agent", href: agentPage(org, a.id), "data-nav": "" }, avatar(a.displayName, true), a.displayName))) : h("span", { class: "cell-muted" }, "No agent");
  const row = (m) => h("tr", {}, h("td", { class: "cell-mono" }, m.name), h("td", {}, providerName(m.provider)), h("td", {}, usedBy(m)),
    h("td", {}, m.default ? h("span", { class: "tag tone-info" }, icon("check"), "Default") : ""),
    h("td", { class: "cell-actions" }, more("More for " + m.name, [
      ["Change its prices", "pencil", () => modelDialog(org, providers, m, refresh)],
      ...(m.default ? [] : [["Make it the default", "check", () => api("PUT", modelPath(org, m), { inputPrice: m.inputPrice, outputPrice: m.outputPrice, default: true },
        { "If-Match": `"${m.version}"` }).then(refresh, fail)]]),
      ["Remove " + m.name, "trash-2", () => ask("Remove " + m.name + "?", "Agents can no longer pick it. The API refuses while an agent runs on it.", "Remove",
        () => api("DELETE", modelPath(org, m), undefined, { "If-Match": `"${m.version}"` }).then(refresh)), true]])));
  return h("table", { class: "table settings-models" }, h("thead", {}, h("tr", {}, ["Model", "Provider", "Used by", "Default"].map((t) => h("th", {}, t)), h("th", { "aria-label": "Actions" }))),
    h("tbody", {}, models.map(row)));
}

// modelDialog adds a model of one of the organization's providers, as the provider names it, with what a million
// tokens it reads and writes cost, or changes model m's prices; either may make it the default a new agent starts on.
function modelDialog(org, providers, m, refresh) {
  const provider = h("select", { required: true, disabled: !!m }, providers.map((p) => h("option", { value: p.name, selected: m && p.name === m.provider }, providerName(p.name))));
  const name = input({ required: true, maxlength: 200, value: m ? m.name : "", placeholder: "claude-opus-5-5", disabled: !!m });
  const price = (value, placeholder) => input({ required: true, inputmode: "decimal", pattern: "\\d{1,5}(\\.\\d{1,6})?", value: value || "", placeholder });
  const inputPrice = price(m && m.inputPrice, "3.00");
  const outputPrice = price(m && m.outputPrice, "15.00");
  const preferred = h("input", { type: "checkbox", checked: m ? m.default : false });
  openDialog({ title: m ? m.name : "Add model", action: m ? "Save" : "Add model",
    body: [field("Provider", selectBox(provider), "Connected in Providers above; its secret reaches the model."), field("Model", name, "As the provider names it."),
      h("div", { class: "settings-models-prices" }, field("Input price", inputPrice, "$ per million input tokens"), field("Output price", outputPrice, "$ per million output tokens")),
      checkbox(preferred, "Default model for new agents")],
    submit: () => api("PUT", modelPath(org, { provider: provider.value, name: name.value.trim() }),
      { inputPrice: inputPrice.value, outputPrice: outputPrice.value, ...(preferred.checked ? { default: true } : {}) },
      m ? { "If-Match": `"${m.version}"` } : { "If-None-Match": "*" }).then(refresh) });
  (m ? inputPrice : name).focus();
}

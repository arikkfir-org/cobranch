import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { size, when } from "../../lib/format.js";
import { icon } from "../../lib/icons.js";
import { roleSelect } from "../../lib/organization.js";
import { go } from "../../lib/router.js";
import { base, setOrgs } from "../../lib/state.js";

export const nav = { label: "Settings", order: 90 };
export default ({ org, main, can }) => showSettings(org, main, can);

// showSettings is an organization's usage against its limits, for anyone who reads it, and, with org.manage, the
// form that changes its settings and its model providers.
async function showSettings(org, main, can) {
  const usage = await api("GET", "/orgs/" + org.slug + "/usage");
  const sections = [h("h1", {}, "Settings"), usagePanel(org, usage)];
  if (can("org.manage")) {
    const error = h("p", { class: "alert", hidden: true });
    const fail = (err) => { error.textContent = err.message; error.hidden = false; };
    sections.push(...settingsForm(org), error, await providersSection(org, can, fail, () => showSettings(org, main, can)));
  }
  main.replaceChildren(...sections);
}

// bytes is a size in binary units, as limits are written: 1 GiB, 2.5 MiB.
const bytes = (n) => size(n).replace(".0 ", " ");

// gauge is one limit's usage: what is used against the limit, on a meter that warns at 90% and turns to danger at
// the limit; a limit of 0 caps nothing, and shows no meter.
function gauge(label, usage, format) {
  const head = h("div", { class: "row" }, h("strong", {}, label),
    h("span", {}, usage.limit ? format(usage.used) + " of " + format(usage.limit) : format(usage.used) + " · no limit"));
  if (!usage.limit) return h("div", { class: "gauge" }, head);
  const share = usage.used / usage.limit;
  const tone = share >= 1 ? "danger" : share >= 0.9 ? "warning" : "";
  const meter = h("div", { class: "meter", role: "meter", "aria-label": label, "aria-valuemin": "0", "aria-valuemax": String(usage.limit),
    "aria-valuenow": String(usage.used), "data-tone": tone }, h("span", { style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" }));
  const note = tone === "danger" ? "At its limit: what would add more is refused. An administrator can raise it."
    : tone === "warning" ? "Near its limit." : null;
  return h("div", { class: "gauge" }, head, meter, note ? h("span", { class: "muted" }, note) : null);
}

// usagePanel is what the organization takes of what its limits cap, with each repository the viewer reads and its
// Git data, largest first.
function usagePanel(org, usage) {
  const list = [...usage.byRepository].sort((a, b) => b.gitData - a.gitData || a.slug.localeCompare(b.slug));
  const unread = usage.repositories.used - list.length;
  const row = (r) => h("tr", {}, h("td", {}, h("a", { href: base + org.slug + "/" + r.slug, "data-nav": "" }, r.name)), h("td", { class: "number" }, bytes(r.gitData)));
  return h("section", { class: "panel" }, h("h3", {}, "Usage"),
    gauge("Repositories", usage.repositories, String), gauge("Git data", usage.gitData, bytes),
    list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", { class: "number" }, "Git data"))), h("tbody", {}, list.map(row))) : null,
    unread > 0 ? h("p", { class: "muted" }, (unread === 1 ? "1 repository you can't read counts" : unread + " repositories you can't read count") + " toward these too.") : null);
}

// settingsForm changes the organization's name, slug, membership role, join requests and identity providers.
function settingsForm(org) {
  const error = h("p", { class: "alert", hidden: true });
  const saved = h("p", { class: "notice", hidden: true }, "Saved.");
  const name = h("input", { name: "name", required: true, value: org.name });
  const slug = h("input", { name: "slug", required: true, value: org.slug, pattern: "[a-z0-9]+(-[a-z0-9]+)*", minlength: 2, maxlength: 39 });
  const role = roleSelect(org.membershipRoleId);
  const accepts = h("input", { name: "acceptsJoinRequests", type: "checkbox", checked: org.acceptsJoinRequests });
  const providers = h("input", { name: "providers", value: (org.providers || []).join(", "), placeholder: "All of this installation's" });
  const issueKey = h("input", { name: "issueKey", required: true, value: org.issueKey, pattern: "[A-Z][A-Z0-9]{0,9}", maxlength: 10,
    oninput: (e) => { e.target.value = e.target.value.toUpperCase(); } });
  const form = h("form", { onsubmit: async (e) => {
    e.preventDefault();
    error.hidden = saved.hidden = true;
    const list = providers.value.split(",").map((s) => s.trim()).filter(Boolean);
    try {
      const updated = await api("PATCH", "/orgs/" + org.slug, { name: name.value, slug: slug.value, membershipRoleId: role.value, acceptsJoinRequests: accepts.checked, providers: list, issueKey: issueKey.value });
      setOrgs((await api("GET", "/me/organizations")).organizations);
      if (updated.slug !== org.slug) return go("/" + updated.slug + "/settings");
      saved.hidden = false;
      org = updated;
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
    }
  } },
    h("label", {}, "Name", name),
    h("label", {}, "Slug", h("span", {}, "Its URL. The old slug keeps pointing here after a change."), slug),
    h("label", {}, "Membership role", h("span", {}, "The role everyone who joins gets."), role),
    h("label", { class: "check" }, accepts, "Accept join requests by slug"),
    h("label", {}, "Issue key", h("span", {}, "What its issues' identifiers start with, such as " + org.issueKey + "-42: 1 to 10 uppercase letters and digits, from a letter. " +
      "A change renames every issue; links to the old identifiers still open them."), issueKey),
    h("label", {}, "Identity providers", h("span", {}, "The providers members may sign in with, by ID, comma-separated; empty for all of this installation's."), providers),
    h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save")));
  return [error, saved, h("section", { class: "panel" }, form)];
}

// providersSection lists the model providers the organization added, each with the secret its API key is in, and the
// forms that add one the installation knows, name another key, and remove one no agent runs on. A key is one of the
// organization's own secrets, which a viewer who manages secrets picks; render draws the page again.
async function providersSection(org, can, fail, render) {
  const path = "/orgs/" + org.slug + "/model-providers";
  const [{ modelProviders: list, available }, keys] = await Promise.all([api("GET", path),
    can("secrets.manage") ? api("GET", "/orgs/" + org.slug + "/secrets").then((d) => d.secrets) : []]);
  const item = (name) => path + "/" + encodeURIComponent(name);
  const keySelect = (selected) => h("select", { required: true }, keys.map((s) => h("option", { value: s.id, selected: s.id === selected }, s.name)));
  const row = (p) => {
    const tr = h("tr", {}, h("td", {}, h("code", {}, p.name)),
      h("td", {}, p.broken ? h("span", { class: "badge tone-danger" }, icon("circle-x"), "Its key is gone") : p.secretName ? h("code", {}, p.secretName) : h("span", { class: "muted" }, "A secret you don't manage")),
      h("td", {}, p.createdBy ? p.createdBy.displayName : h("span", { class: "muted" }, "Someone since gone"), h("p", {}, when(p.updatedAt))),
      h("td", { class: "actions" },
        keys.length ? h("button", { class: "button small", type: "button", onclick: () => {
          const key = keySelect(p.secretId);
          const editor = h("tr", {}, h("td", { colspan: 4 }, h("form", { class: "inline with-role", onsubmit: (e) => {
            e.preventDefault();
            api("PUT", item(p.name), { secretId: key.value }, { "If-Match": `"${p.version}"` }).then(render).catch(fail);
          } }, h("label", {}, "Key", key), h("button", { class: "button primary", type: "submit" }, "Save"), " ",
            h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(tr) }, "Cancel"))));
          tr.replaceWith(editor);
        } }, "Change key") : null, " ",
        h("button", { class: "button small danger", type: "button", title: "Remove " + p.name,
          onclick: () => confirm("Remove " + p.name + "? Agents on it must move to another provider first.") && api("DELETE", item(p.name), undefined, { "If-Match": `"${p.version}"` }).then(render).catch(fail) }, "Remove")));
    return tr;
  };
  const left = available.filter((name) => !list.some((p) => p.name === name));
  const section = h("section", { class: "panel" }, h("h3", {}, "Model providers"),
    h("p", {}, "The providers this organization's agents run on, each with its API key, kept as one of the organization's secrets. An agent's tasks run only while the agent may read its provider's key."),
    list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Provider"), h("th", {}, "Key"), h("th", {}, "Set by"), h("th", {}))), h("tbody", {}, list.map(row)))
      : h("p", {}, "No providers yet."));
  if (!left.length) return section;
  if (!keys.length) {
    section.append(h("p", { class: "muted" }, can("secrets.manage") ? ["Add a provider's API key as one of the organization's ", h("a", { href: base + org.slug + "/secrets", "data-nav": "" }, "secrets"), " to add the provider."]
      : "Adding a provider takes picking its key among the organization's secrets, which only those who manage them see."));
    return section;
  }
  const provider = h("select", { required: true }, left.map((name) => h("option", { value: name }, name)));
  const key = keySelect();
  section.append(h("form", { class: "inline with-role", onsubmit: (e) => {
    e.preventDefault();
    api("PUT", item(provider.value), { secretId: key.value }, { "If-None-Match": "*" }).then(render).catch(fail);
  } }, h("label", {}, "Provider", provider), h("label", {}, "Key", key), h("button", { class: "button primary", type: "submit" }, "Add")));
  return section;
}

import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { banner, failure, field, input, textarea } from "../../../lib/forms.js";
import { go, render } from "../../../lib/router.js";

export const tab = { label: "General", order: 0 };
export default ({ org, repo, path, can }) => showGeneral(org, repo, path, can("repo.manage"));

// sectionHeader is a section's title over the line on what it holds.
const sectionHeader = (title, description) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)));

// visibilityOf is who reads the repository, as its Visibility field says it.
const visibilityOf = (org, repo) => repo.visibility === "internal" ? "Internal · others of " + org.name + " too"
  : "Private · those granted a role on it, and " + org.name + "'s Owners";

// showGeneral is how the repository is named and described, in a card with Save for a viewer with repo.manage, and
// who reads it, which its grants decide (the Access tab); anyone else sees them as they are, with nothing to change.
function showGeneral(org, repo, path, manage) {
  const error = failure();
  const saved = banner("success", null, "Saved.");
  saved.hidden = true;
  const name = input({ name: "name", required: true, maxlength: 100, value: repo.name, disabled: !manage });
  const slug = input({ name: "slug", required: true, value: repo.slug, placeholder: "website", pattern: "[a-z0-9][a-z0-9._\\-]*", maxlength: 100, disabled: !manage });
  const description = textarea({ name: "description", maxlength: 500, rows: 4, disabled: !manage });
  description.value = repo.description || "";
  const save = h("button", { class: "btn btn-primary", type: "submit" }, "Save");
  const submit = async (e) => {
    e.preventDefault();
    error.el.hidden = saved.hidden = true;
    save.disabled = true;
    try {
      const updated = await api("PATCH", path, { slug: slug.value, name: name.value, description: description.value });
      if (updated.slug !== repo.slug) return go("/" + org.slug + "/" + updated.slug + "/settings");
      // The whole page draws again, so the heading takes a new name and description too.
      await render();
      document.querySelector("main .banner-success").hidden = false;
    } catch (err) {
      error.fail(err);
      save.disabled = false;
    }
  };
  return [error.el, saved,
    sectionHeader("General", "Name, URL name and description. Visibility follows who holds the repository's roles, under Access."),
    h("form", { class: "card repo-general", onsubmit: submit },
      h("div", { class: "card-body" },
        field("Name", name, "How the repository is named in lists and titles."),
        field("URL name", slug, org.slug + "/" + repo.slug + " · the URL and clone path. The old one keeps redirecting here until another repository takes it."),
        field("Description", description),
        field("Visibility", input({ value: visibilityOf(org, repo), disabled: true, "aria-readonly": "true" }),
          "Decided by grants: it is Internal once others of " + org.name + " read it, by a grant or a role there. Change it under Access.")),
      manage ? h("footer", { class: "card-footer" }, save) : null)];
}

import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { repoSlugInput } from "../../lib/repository.js";
import { go } from "../../lib/router.js";

export const nav = { label: "Settings", order: 90 };
export const crumbs = [{ label: "Settings" }];
export const allowed = (can) => can("repo.manage");
export default ({ org, repo, path, fail }) => repositorySettings(org, repo, path, fail);

// repositorySettings changes repo's slug, name and description.
function repositorySettings(org, repo, path, fail) {
  const saved = h("p", { class: "notice", hidden: true }, "Saved.");
  const slug = repoSlugInput(repo.slug);
  const name = h("input", { name: "name", required: true, maxlength: 100, value: repo.name });
  const description = h("input", { name: "description", maxlength: 500, value: repo.description });
  const form = h("form", { onsubmit: async (e) => {
    e.preventDefault();
    saved.hidden = true;
    try {
      const updated = await api("PATCH", path, { slug: slug.value, name: name.value, description: description.value });
      if (updated.slug !== repo.slug) return go("/" + org.slug + "/" + updated.slug + "/settings");
      saved.hidden = false;
    } catch (err) {
      fail(err);
    }
  } },
    h("label", {}, "Name", name),
    h("label", {}, "Slug", h("span", {}, "Its URL. The old slug keeps pointing here until another repository of " + org.name + " takes it."), slug),
    h("label", {}, "Description", description),
    h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save")));
  return [saved, h("section", { class: "panel" }, form)];
}

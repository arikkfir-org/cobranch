import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { repoSlugInput } from "../../lib/repository.js";
import { go } from "../../lib/router.js";

export const allowed = (can) => can("repos.create");
export const view = "organization.new-repository";
export const crumbs = [{ label: "New repository" }];
export default ({ org, main }) => showNewRepository(org, main);

// The form that makes a repository, whose creator becomes its Maintainer.
function showNewRepository(org, main) {
  const error = h("p", { class: "alert", hidden: true });
  const slug = repoSlugInput("");
  const name = h("input", { name: "name", maxlength: 100, placeholder: "Website" });
  const description = h("input", { name: "description", maxlength: 500 });
  main.replaceChildren(h("h1", {}, "New repository"), error, h("section", { class: "panel" },
    h("form", { onsubmit: (e) => {
      e.preventDefault();
      api("POST", "/orgs/" + org.slug + "/repos", { slug: slug.value, name: name.value || undefined, description: description.value })
        .then((r) => go("/" + org.slug + "/" + r.slug)).catch((err) => { error.textContent = err.message; error.hidden = false; });
    } },
      h("label", {}, "Slug", h("span", {}, "Its URL, in " + org.name + ": lowercase letters, digits, dots, underscores and hyphens."), slug),
      h("label", {}, "Name", h("span", {}, "Its slug unless you name it."), name),
      h("label", {}, "Description", description),
      h("p", {}, "You become its Maintainer. Others of " + org.name + " read it once you grant them a role on it, or through an organization role that reads every repository."),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Create")))));
}

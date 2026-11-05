import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { failure, field, input, textarea } from "../../lib/forms.js";
import { pageHeading } from "../../lib/layout.js";
import { go } from "../../lib/router.js";
import { base } from "../../lib/state.js";

export const allowed = (can) => can("repos.create");
export const view = "organization.new-repository";
export const crumbs = ({ root }) => [{ label: "Repositories", href: root + "/repos" }, { label: "New" }];
export const heading = () => pageHeading({ title: "New repository", description: "A repository holds code, branches, issues, jobs and the conversations around them." });
export default ({ org, main }) => showNewRepository(org, main);

// The form that makes a repository, named by its slug, whose creator becomes its Maintainer; it opens the repository
// once made.
function showNewRepository(org, main) {
  const error = failure();
  const where = (slug) => "Lower case letters, digits, dots, underscores and hyphens. It becomes the URL, " + org.slug + "/" + (slug || "website") + ", and the clone path.";
  const help = h("span", {}, where(""));
  const slug = input({ name: "slug", required: true, placeholder: "website", pattern: "[a-z0-9][a-z0-9._\\-]*", maxlength: 100, autocomplete: "off",
    oninput: () => { help.textContent = where(slug.value); } });
  const description = textarea({ name: "description", maxlength: 500, rows: 4 });
  const create = h("button", { class: "btn btn-primary", type: "submit" }, "Create repository");
  main.replaceChildren(error.el, h("form", { class: "card new-repository", onsubmit: (e) => {
    e.preventDefault();
    create.disabled = true;
    api("POST", "/orgs/" + org.slug + "/repos", { slug: slug.value, description: description.value })
      .then((r) => go("/" + org.slug + "/" + r.slug)).catch((err) => { error.fail(err); create.disabled = false; });
  } },
  h("div", { class: "card-body" }, field("Name", slug, help), field("Description", description, "One line, shown in lists.")),
  h("footer", { class: "card-footer" }, h("a", { class: "btn", href: base + org.slug + "/repos", "data-nav": "" }, "Cancel"), create)));
  slug.focus();
}

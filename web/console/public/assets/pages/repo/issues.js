import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { issueList, listTabLinks } from "../../lib/issues.js";

// nav makes Issues one of a repository's tabs, after Delivery, for a viewer who reads the organization's issues, and
// count counts the open ones about the repository.
export const nav = { label: "Issues", order: 40 };
export const allowed = (can, orgCan) => orgCan("issues.read");
export const accepts = (rest) => !rest.length;
export const crumbs = ({ root }) => [{ label: "Issues", href: root + "/issues" }];
export const count = ({ org, repo }) => api("GET", "/orgs/" + org.slug + "/issues?" + new URLSearchParams({ limit: "1", repository: repo.id }))
  .then((d) => d.counts.open);

// The organization's issues about the repository (Figma "Repository · Issues") under the tabs Open, Closed and All:
// the organization's list (lib/issues.js) without its Repository filter and column. An issue opens on the
// organization's Issues, where it lives.
export default async ({ org, repo, orgCan }) => {
  const tabs = h("nav", { class: "tab-bar", "aria-label": "Issues" }, listTabLinks("/" + org.slug + "/" + repo.slug + "/issues", new URLSearchParams(location.search)));
  return [tabs, ...await issueList({ org, can: orgCan, tabs, repository: repo })];
};

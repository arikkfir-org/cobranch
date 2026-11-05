import { api } from "../../lib/api.js";
import { branchBadge, checkShort, processingWhat } from "../../lib/branches.js";
import { h } from "../../lib/dom.js";
import { fromNow, plural, shortSha } from "../../lib/format.js";
import { authorAvatar, icon } from "../../lib/icons.js";
import { config, me } from "../../lib/state.js";

export const nav = { label: "Branches", order: 0 };
export const view = "repository.branches";
// count is how many branches the repository has besides main, each a request to merge into it.
export const count = ({ path }) => listBranches(path).then((branches) => branches.filter((b) => b.status).length);
export default (ctx) => repositoryBranches(ctx);

// listBranches reads the repository's branches at path, once for a page and its tab's count.
let listed = null;
function listBranches(path) {
  if (!listed || listed.path !== path || Date.now() - listed.at > 2000) listed = { path, at: Date.now(), branches: api("GET", path + "/branches").then((d) => d.branches) };
  return listed.branches;
}

// repositoryBranches is the repository's first tab, as the Repository screen draws it: its branches, filtered by
// name and by what they are, main above the rest, and beside them what a merge into main needs and the environments
// branches deliver to. A repository nothing was pushed to says how to push its first commit.
export async function repositoryBranches(ctx) {
  const { path, at, root } = ctx;
  const [branches, environments] = await Promise.all([listBranches(path),
    api("GET", path + "/environments").then((d) => d.environments, () => [])]);
  if (!branches.length) return [h("section", { class: "card" }, h("div", { class: "empty" }, h("span", { class: "empty-icon" }, icon("git-branch")),
    h("h2", { class: "empty-title" }, "No branches yet"),
    h("p", { class: "empty-description" }, "Nothing has been pushed to this repository. Clone it and push main: every other branch is then a request to merge into it.")))];
  const main = branches.find((b) => !b.status);
  const others = branches.filter((b) => b.status);
  const rows = others.map((b) => [b, branchRow(at, b)]);
  const none = h("li", { class: "branch-row", hidden: true }, h("span", { class: "row-meta" }, "No branches match."));
  const list = others.length ? h("ul", { class: "list" }, rows.map(([, el]) => el), none)
    : h("div", { class: "card-body" }, h("p", { class: "muted" }, "No branches besides main yet. Push one, and it shows here as a request to merge into main."));
  return [h("div", { class: "branches-page" },
    h("div", { class: "branches-main" }, branchFilters(rows, none),
      main ? h("ul", { class: "list" }, branchRow(at, main)) : null,
      h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Branches"), h("span", { class: "card-meta" }, "Into main")), list)),
    h("div", { class: "branches-side" }, main ? mergeCard(ctx, main) : null, environmentsCard(at, root, environments)))];
}

// branchFilters filters rows, each a branch and its row, by a part of its name and by what it is: all, Ready, Blocked,
// or the viewer's own, those whose head they wrote; none shows when no row is left.
function branchFilters(rows, none) {
  const groups = [["All", () => true], ["Ready", (b) => b.status === "ready"], ["Blocked", (b) => b.status === "blocked"],
    ["Mine", (b) => !!(b.commit && b.commit.author.principal && b.commit.author.principal.id === me.id)]];
  let group = groups[0], text = "";
  const apply = () => {
    let shown = 0;
    for (const [b, el] of rows) {
      el.hidden = !(group[1](b) && b.name.toLowerCase().includes(text));
      if (!el.hidden) shown++;
    }
    none.hidden = shown > 0 || !rows.length;
  };
  const buttons = groups.map((g) => h("button", { class: "btn btn-sm" + (g === group ? "" : " btn-ghost"), type: "button", "aria-pressed": String(g === group),
    onclick: () => {
      group = g;
      buttons.forEach((button, i) => {
        button.className = "btn btn-sm" + (groups[i] === g ? "" : " btn-ghost");
        button.setAttribute("aria-pressed", String(groups[i] === g));
      });
      apply();
    } }, g[0] + " " + rows.filter(([b]) => g[1](b)).length));
  return h("div", { class: "branch-filters" },
    h("label", { class: "input branch-filter" }, icon("search"), h("input", { type: "search", placeholder: "Filter branches", "aria-label": "Filter branches",
      oninput: (e) => { text = e.target.value.trim().toLowerCase(); apply(); } })),
    h("div", { class: "branch-groups", role: "group", "aria-label": "Show branches" }, buttons));
}

// branchRow is a branch as a Branch row: its name, over what it would merge, when it moved and by whose head, and
// where it stands; its status, and the avatar of its head's author.
function branchRow(at, b) {
  const author = b.commit && b.commit.author;
  const meta = branchMeta(b);
  return h("li", { class: "branch-row" }, icon("git-branch"),
    h("div", { class: "row-text" }, h("a", { class: "row-title", href: at.branchPage(b.name), "data-nav": "" }, b.name), h("span", { class: "row-meta", title: meta }, meta)),
    b.status ? branchBadge(b.status) : null, author ? authorAvatar(author) : null);
}

// branchMeta is the line under a branch's name in its row: main's head, or what a branch would merge, when it moved
// and by whose head, and what holds it back or how it stands.
function branchMeta(b) {
  const author = b.commit && b.commit.author;
  const by = author ? " by " + (author.principal ? author.principal.displayName : author.name) : "";
  if (!b.status) return "Default branch · " + shortSha(b.head) + " · updated " + fromNow(b.updatedAt) + by;
  if (b.status === "merged") return "Merged" + (b.merge && b.merge.by ? " by " + b.merge.by.displayName : "") + " " + fromNow(b.merge ? b.merge.at : b.updatedAt) +
    " · delete it to tear down its environments";
  if (b.ahead === undefined) return "Pushed " + fromNow(b.updatedAt) + by + " · working out what it would merge";
  if (!b.ahead) return "No commits ahead of " + b.base + " · created " + fromNow(b.createdAt);
  const notes = { blocked: () => { const c = b.checks.find((c) => !c.met); return c ? checkShort(b, c) : null; }, processing: () => processingWhat(b),
    ready: () => { const a = b.checks.find((c) => c.kind === "approvals"); return a && a.count ? plural(a.count, "approval") : null; } };
  const note = notes[b.status] && notes[b.status]();
  return plural(b.ahead, "commit") + " · updated " + fromNow(b.updatedAt) + by + (note ? " · " + note : "");
}

// mergeCard is what a merge into main needs (Fact rows, until the Patterns page draws them): the annotations main's
// /cobranch.yaml requires on a branch's head and the AI reviewer's approval, the reviews, linear history, and where to
// clone the repository from.
export function mergeCard({ org, repo }, main) {
  const r = main.requirements;
  const requires = r && (r.annotations.length || (r.reviews && r.reviews.ai)) ? h("span", { class: "fact-row-tags" }, r.annotations.map((n) => h("span", { class: "tag" }, n)),
    r.reviews && r.reviews.ai ? h("span", { class: "tag tone-ai" }, icon("sparkles"), "AI review") : null) : "Nothing besides being up to date";
  const reviews = !r || !r.reviews ? "Not required" : (r.reviews.approvals ? plural(r.reviews.approvals, "approval") + ", no" : "No") + " open change requests";
  const url = config.gitUrl ? config.gitUrl + "/" + org.slug + "/" + repo.slug + ".git" : config.gitSshUrl ? config.gitSshUrl + "/" + org.slug + "/" + repo.slug + ".git" : null;
  return h("section", { class: "card" },
    h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Merge into main")),
    h("dl", { class: "fact-rows" }, factRow("Requires", requires), factRow("Reviews", reviews),
      factRow("History", r && r.linearHistory ? "Linear history required" : "Linear history not required"),
      url ? factRow("Clone", h("code", { class: "fact-row-mono", title: url }, url.replace(/^https?:\/\//, ""))) : null));
}
// factRow is a label and its value, as a Fact row.
const factRow = (label, value) => h("div", { class: "fact-row" }, h("dt", {}, label), h("dd", {}, value));

// environmentsCard lists the environments the repository's branches deliver to, each its branch's name and how it
// stands (Environment rows, until the Patterns page draws them), with a link to Delivery.
function environmentsCard(at, root, environments) {
  const targets = new Set(environments.map((e) => e.target.id)).size;
  return h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Environments"),
    h("a", { class: "card-meta card-link", href: root + "/environments", "data-nav": "" }, "Delivery")),
  environments.length ? h("ul", { class: "list" }, environments.map((e) => h("li", { class: "environment-row" },
    h("a", { class: "row-mono", href: at.environment(e.id), "data-nav": "", title: e.target.displayName + " · " + e.branch }, targets > 1 ? e.target.displayName + " · " + e.branch : e.branch),
    environmentTag(e))))
    : h("div", { class: "card-body" }, h("p", { class: "muted" }, "None yet. Each target main's /cobranch.yaml declares under delivery makes one for every branch it matches.")));
}

// environmentTag is how an environment stands, as a tag: Paused, its health when it is degraded or missing, Syncing
// while a delivery runs, else Synced or Out of sync.
function environmentTag(e) {
  const [tone, word] = e.paused ? ["neutral", "Paused"] : e.health === "degraded" ? ["danger", "Degraded"] : e.health === "missing" ? ["danger", "Missing"]
    : e.latest && ["queued", "rendering", "applying"].includes(e.latest.state) ? ["info", "Syncing"] : e.sync === "synced" ? ["success", "Synced"] : ["neutral", "Out of sync"];
  return h("span", { class: "tag tone-" + tone }, word);
}

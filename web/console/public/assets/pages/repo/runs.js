import { api } from "../../lib/api.js";
import { branchSubject } from "../../lib/chat.js";
import { h } from "../../lib/dom.js";
import { capitalized, whenExactly } from "../../lib/format.js";
import { render } from "../../lib/router.js";
import { refName, runActions, runBadge, runsPanel, triggered } from "../../lib/runs.js";
import { follows } from "../../lib/state.js";

export const nav = { label: "Runs", order: 20 };
export const accepts = (rest) => rest.length <= 1;
export const heading = (rest) => !rest.length;
export const crumbs = ({ root, rest }) => [{ label: "Runs", href: root + "/runs" }, ...(rest.length ? [{ label: "#" + rest[0] }] : [])];
// subject is a run's branch's conversation, on the run's page; a tag's run has none.
export const subject = ({ org, repo, path, at, rest, can }) => rest.length ? api("GET", path + "/runs/" + encodeURIComponent(rest[0]))
  .then((r) => (r.ref.startsWith("refs/heads/") ? branchSubject(org, repo, path, at, r.ref.slice("refs/heads/".length), can) : null)) : null;
export default async ({ path, at, rest, can, fail }) => rest.length ? repositoryRun(path, at, rest[0], can, fail) : [await runsPanel(path, at, can, fail, "")];

// repositoryRun is a run's page: its state and why, its job, ref and commit, what queued it, who started it, when it
// was queued, started and ended, its actions, and its job's definition as the run keeps it.
async function repositoryRun(path, at, number, can, fail) {
  let r;
  try {
    r = await api("GET", path + "/runs/" + encodeURIComponent(number));
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [h("section", { class: "panel" }, h("h3", {}, "Run"), h("p", {}, "This repository has no run #" + number + "."))];
  }
  const d = r.definition;
  const facts = h("dl", { class: "facts" },
    h("dt", {}, "State"), h("dd", {}, runBadge(r.state), r.reason ? [" ", capitalized(r.reason)] : null),
    h("dt", {}, "On"), h("dd", {}, refName(r.ref), " at ", h("a", { href: at.commit(r.sha), "data-nav": "" }, h("code", {}, r.sha))),
    h("dt", {}, "Queued"), h("dd", {}, "By " + triggered(r) + ", " + whenExactly(r.queuedAt)),
    r.causeRun ? [h("dt", {}, "Caused by"), h("dd", {}, h("a", { href: at.run(r.causeRun), "data-nav": "" }, "#" + r.causeRun))] : null,
    r.rerunOf ? [h("dt", {}, "Attempt"), h("dd", {}, String(r.attempt) + " of ", h("a", { href: at.run(r.rerunOf), "data-nav": "" }, "#" + r.rerunOf))] : null,
    r.startedBy ? [h("dt", {}, r.rerunOf ? "Re-run by" : "Started by"), h("dd", {}, r.startedBy.displayName)] : null,
    r.startedAt ? [h("dt", {}, "Started"), h("dd", {}, whenExactly(r.startedAt))] : null,
    r.endedAt ? [h("dt", {}, "Ended"), h("dd", {}, whenExactly(r.endedAt))] : null);
  const work = d.kind === "script" ? [h("p", {}, "A script, in ", d.image ? h("code", {}, d.image) : "the installation's image", d.checkout ? ", in a checkout of the commit:" : ":"), h("pre", { class: "message" }, d.script)]
    : d.kind === "pod" ? [h("p", {}, "A pod", d.checkout ? ", in a checkout of the commit:" : ":"), h("pre", { class: "message" }, JSON.stringify(d.pod, null, 2))]
    : [h("p", {}, "A webhook, posted to ", h("code", {}, d.webhook.url), ".")];
  const definition = h("dl", { class: "facts" },
    h("dt", {}, "Permissions"), h("dd", {}, d.permissions.map((p, i) => [i ? " " : null, h("code", {}, p)])),
    h("dt", {}, "Timeout"), h("dd", {}, d.timeout || "The installation's"),
    d.secrets ? [h("dt", {}, "Secrets"), h("dd", {}, Object.keys(d.secrets).map((env, i) => [i ? " " : null, h("code", {}, env)]))] : null);
  return [h("section", { class: "panel" }, h("div", { class: "row" }, h("h2", {}, h("code", {}, r.job.displayName), " #" + r.number), h("div", {}, runActions(path, at, r, can, fail))), facts),
    await runLogs(path, r),
    h("section", { class: "panel" }, h("h3", {}, "Job"), h("p", {}, "As ", h("code", {}, "/cobranch.yaml"), " declared it when the run was queued."), ...work, definition)];
}

// runLogs is a run's logs, each container's, the checkout's first, with the values the run masks hidden: as the
// controller kept them once it ended, and while it runs, read from its pod again every five seconds, until it ends,
// when the page draws again, or the panel leaves the page.
async function runLogs(path, r) {
  const url = path + "/runs/" + r.number + "/logs";
  const status = h("span", { class: "muted" });
  const body = h("div", { class: "logs" });
  const panel = h("section", { class: "panel" }, h("div", { class: "row" }, h("h3", {}, "Logs"), status), body);
  const none = r.definition.kind === "webhook" ? "A webhook's run keeps no logs here: its receiver keeps its own."
    : r.state === "running" ? "Nothing logged yet." : r.startedAt ? "It kept no logs." : "None until it starts.";
  const draw = (logs) => body.replaceChildren(...(logs.length ? logs.map((l) => h("div", {}, h("code", {}, l.container), h("pre", { class: "log" }, l.text)))
    : [h("p", { class: "muted" }, none)]));
  try {
    draw(await api("GET", url).then((d) => d.logs));
  } catch (err) { // the run's page still draws, its logs saying why they're missing
    body.replaceChildren(h("p", { class: "alert" }, "The logs couldn't be read: " + err.message));
    return panel;
  }
  if (r.state !== "running") return panel;
  status.textContent = "Live";
  const stop = new AbortController();
  follows(stop);
  (async () => {
    while (!stop.signal.aborted) {
      await new Promise((resolve) => setTimeout(resolve, 5000));
      if (stop.signal.aborted || !panel.isConnected) return;
      try {
        const now = await api("GET", path + "/runs/" + r.number, undefined, {}, stop.signal);
        if (!panel.isConnected) return;
        if (now.state !== "running") return void render(); // it ended: the page draws its end and its archived logs
        const logs = await api("GET", url, undefined, {}, stop.signal).then((d) => d.logs);
        if (!panel.isConnected) return;
        if (logs.length) draw(logs);
        status.textContent = "Live";
      } catch (err) {
        if (stop.signal.aborted) return;
        status.textContent = "Reconnecting…";
      }
    }
  })();
  return panel;
}

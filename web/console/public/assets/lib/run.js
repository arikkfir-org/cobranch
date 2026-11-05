import { annotationStatuses } from "./annotations.js";
import { api } from "./api.js";
import { h } from "./dom.js";
import { capitalized, fromNow, shortSha, subject, whenExactly } from "./format.js";
import { banner } from "./forms.js";
import { icon } from "./icons.js";
import { recrumb } from "./layout.js";
import { render } from "./router.js";
import { duration, noRuns, runActions, runStates, runStatuses } from "./runs.js";
import { base, config, follows } from "./state.js";

// stepStates are how a step's state draws in its row and its log's header: its icon, its tone, and, for a step that
// hasn't started, quiet.
const stepStates = {
  succeeded: { word: "Succeeded", icon: "circle-check", tone: "success" },
  failed: { word: "Failed", icon: "circle-x", tone: "danger" },
  running: { word: "Running", icon: "loader-circle", tone: "info" },
  waiting: { word: "Not started", icon: "circle-dashed", quiet: true },
  unknown: { word: "Ran before runs kept their steps' states", icon: "circle", quiet: true },
};
// active are the states of a run that hasn't ended, whose page reads it again until it does.
const active = ["waiting", "queued", "running"];

// repositoryRun is a run's page (Repository · Jobs · Run): its job, number and status, with Start, Cancel or Re-run
// and Download logs; a line of its ref, commit, cause, when it started, how long it ran and which attempt it is; a
// banner when it failed or was cancelled; and its steps beside the log of the one selected, or its job's definition.
// A webhook's run shows where it posted, and a run that hasn't started an Empty state. While it waits, is queued or
// runs, the page reads it again every five seconds, its steps and logs too while it runs, and draws again once its
// state changes.
export async function repositoryRun({ org, repo, path, at, rest, can, root, fail }) {
  const runPath = path + "/runs/" + encodeURIComponent(rest[0]);
  let r;
  try {
    r = await api("GET", runPath);
  } catch (err) {
    if (err.status !== 404 && err.status !== 400) throw err;
    return [noRuns("search", "No run #" + rest[0], "This repository has no run with that number. Jobs lists every run it has.")];
  }
  recrumb([{ label: org.name, href: base + org.slug }, { label: repo.slug, href: root }, { label: r.job.displayName + " #" + r.number }]);
  const ran = r.definition.kind !== "webhook" && Boolean(r.startedAt);
  // A commit or a first attempt that can't be read leaves the line without its subject or its pusher's name; steps
  // that can't be read, as from a rest-server of the release before while a rollout replaces it, leave those its logs
  // name; logs that can't be read leave the log card saying why.
  const [commit, first, steps, logs] = await Promise.all([
    api("GET", path + "/commits/" + r.sha).catch(() => null),
    r.rerunOf ? api("GET", path + "/runs/" + r.rerunOf).catch(() => null) : r,
    ran ? api("GET", runPath + "/steps").then((d) => d.steps, () => []) : [],
    ran ? api("GET", runPath + "/logs").then((d) => d.logs, (err) => err) : [],
  ]);
  const view = { r, commit, first, steps, logs };
  const meta = h("div", { class: "run-meta-slot" });
  const body = h("div", { class: "run-body" });
  const draw = () => {
    meta.replaceChildren(...[metaLine(at, view.r, commit, first), failure(view.r, can)].filter(Boolean));
    body.replaceChildren(...runBody(path, view, select));
  };
  // select shows a step's log, or the definition (step null), and names it in the address, so a reload or a link keeps it.
  const select = (step) => {
    history.replaceState(history.state, "", location.pathname + (step === null ? "?definition" : "?step=" + encodeURIComponent(step)));
    draw();
  };
  draw();
  // An installation that keeps no logs has none to download once the run ended.
  const downloadable = ran && (r.state === "running" || (Array.isArray(logs) && logs.length > 0));
  const header = h("header", { class: "run-header" },
    h("div", { class: "run-title-line" }, icon("bot"), h("h1", { class: "run-title" }, h("span", {}, r.job.displayName), h("span", { class: "run-number" }, "#" + r.number)),
      statusBadge(r), h("div", { class: "run-actions" }, runActions(path, at, r, can, fail), downloadable ? downloadButton(runPath, r, fail) : null)),
    meta);
  if (active.includes(r.state)) follow(runPath, view, header, draw);
  return [h("div", { class: "run-page" }, header, body)];
}

// statusBadge is a run's status as its job's annotation says it (Badge/Annotation), its state and why in its tooltip.
function statusBadge(r) {
  const status = Object.entries(runStatuses).find(([, v]) => v.states.includes(r.state))[0];
  const s = annotationStatuses[status];
  return h("span", { class: "badge tone-" + s.tone, title: runStates[r.state].word + (r.reason ? ": " + capitalized(r.reason) : "") }, icon(s.icon), s.word);
}

// metaLine is the line under a run's title: its branch or tag and its commit, linked, then the commit's subject, what
// queued it, when it started or why it hasn't, how long it ran, and, for a re-run, its attempt, the run it re-runs and
// who re-ran it. It wraps rather than cut what it says.
function metaLine(at, r, commit, first) {
  const tag = r.ref.startsWith("refs/tags/");
  const ref = r.ref.replace(/^refs\/(heads|tags)\//, "");
  const parts = [commit ? subject(commit.message) : null, cause(r, first)];
  if (r.state === "waiting") parts.push("waiting for a member to start it");
  else if (!r.startedAt) parts.push(h("span", { title: whenExactly(r.queuedAt) }, "queued " + fromNow(r.queuedAt) + (r.state === "queued" && r.reason ? "; " + r.reason : "")));
  else {
    // A member who started a run that waited is named, unless they are who pushed it.
    const starter = r.startedBy && !r.rerunOf && r.startedBy.id !== r.trigger.actor ? " by " + r.startedBy.displayName : "";
    parts.push(h("span", { title: whenExactly(r.startedAt) }, "started " + fromNow(r.startedAt) + starter), duration(r));
  }
  if (r.rerunOf) {
    parts.push("attempt " + r.attempt, ["re-run of ", h("a", { href: at.run(r.rerunOf), "data-nav": "" }, "#" + r.rerunOf), r.startedBy ? " by " + r.startedBy.displayName : null]);
  }
  return h("p", { class: "run-meta" }, icon(tag ? "tag" : "git-branch"),
    h("a", { href: tag ? at.tree(ref) : at.branchPage(ref), "data-nav": "", title: (tag ? "Tag " : "Branch ") + ref }, ref),
    h("a", { href: at.commit(r.sha), "data-nav": "", title: r.sha }, shortSha(r.sha)),
    h("span", { class: "run-meta-text" }, parts.filter(Boolean).map((p) => ["· ", p, " "])));
}

// cause says what queued a run, and who did, when the run knows their name: its first attempt's cause, unless a
// member started that one, which hides who pushed.
function cause(r, first) {
  const t = r.trigger;
  const actor = first && first.causedBy && first.causedBy.id === t.actor ? first.causedBy.displayName : null;
  const by = actor ? " by " + actor : "";
  return ({
    push: "pushed" + by,
    tag: "tagged" + by,
    annotation: "after annotation " + t.name + (t.state ? " " + t.state : ""),
    review: "after a review" + (t.state ? " (" + t.state + ")" : "") + by,
    event: "event " + t.name + " posted" + by,
    schedule: "on its schedule",
    manual: "run by hand" + by,
  })[t.kind] || t.kind;
}

// failure is the banner of a run that failed or was cancelled, saying why, and, for a viewer who may re-run it, what
// Re-run does.
function failure(r, can) {
  if (r.state === "cancelled") return banner("danger", null, capitalized(r.reason || "cancelled") + ".");
  if (r.state !== "failed") return null;
  const branch = r.ref.startsWith("refs/heads/");
  const next = [branch && r.trigger.kind === "push" ? "Every push checks the branch again" : null, can("jobs.run") ? "Re-run tries this commit once more" : null].filter(Boolean);
  return banner("danger", null, "Failed: " + (r.reason || "it ended with an error") + "." + (next.length ? " " + next.join("; ") + "." : ""));
}

// runBody is what a run's page shows under its header: a webhook's card; an Empty state while it has no steps; or
// its Steps card beside the selected step's log, or its definition.
function runBody(path, view, select) {
  const { r, steps, logs } = view;
  if (r.definition.kind === "webhook") {
    const url = r.definition.webhook.url;
    return [h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Webhook")),
      h("p", { class: "card-body card-note" }, (r.startedAt ? "Posted to " + url : "Posts to " + url + " once it starts") + ". Its receiver keeps its own logs."))];
  }
  const shown = steps.length ? steps : keptSteps(logs);
  if (!shown.length) return [notStarted(r)];
  const params = new URLSearchParams(location.search);
  const picked = params.has("definition") ? null : shown.find((s) => s.name === params.get("step")) || pick(shown);
  const rows = shown.map((s) => stepRow(s, s === picked, () => select(s.name)));
  rows.push(h("a", { class: "step-row step-row-quiet", href: "?definition", "aria-current": String(picked === null),
    onclick: (e) => { e.preventDefault(); select(null); } }, icon("file-code"), h("span", { class: "step-row-name" }, "Definition")));
  const card = h("section", { class: "card" }, h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Steps"),
    r.startedAt ? h("span", { class: "card-meta" }, duration(r)) : null), h("nav", { "aria-label": "Steps" }, rows));
  return [h("div", { class: "run-columns" }, card, picked ? stepLog(path, r, picked, logs) : definition(r))];
}

// keptSteps are the steps of a run that ended before runs kept their steps: its logs' containers, in their order,
// whose states it never kept.
const keptSteps = (logs) => (Array.isArray(logs) ? logs.map((l) => ({ name: l.container, state: "unknown", exitCode: 0, reason: "" })) : []);

// pick is the step a run's page shows unless the address names one: the first that failed, else the one running,
// else the last that started, else the first.
const pick = (steps) => steps.find((s) => s.state === "failed") || steps.find((s) => s.state === "running") ||
  [...steps].reverse().find((s) => s.startedAt) || steps[0];

// stepRow is a step in the Steps card (Run/Step row): its state's icon, its name and how long it ran; a link to its log.
function stepRow(s, selected, open) {
  const st = stepStates[s.state] || stepStates.unknown;
  const glyph = icon(st.icon, st.word + (s.reason ? ": " + s.reason : ""));
  if (st.tone) glyph.classList.add("tone-" + st.tone);
  return h("a", { class: "step-row" + (st.quiet ? " step-row-quiet" : ""), href: "?step=" + encodeURIComponent(s.name), "aria-current": String(selected),
    onclick: (e) => { e.preventDefault(); open(); } },
  glyph, h("span", { class: "step-row-name" }, s.name), s.startedAt ? h("span", { class: "step-row-time" }, duration(s)) : null);
}

// notStarted is the Empty state of a run with no steps yet: it waits for a member, for the cluster or for its pod,
// or it ended before it started.
function notStarted(r) {
  const says = {
    waiting: "The run starts when a member starts it; its steps and logs appear here.",
    queued: "The run starts when the cluster takes it; its steps and logs appear here.",
    running: "The run's pod is starting; its steps and logs appear here.",
  }[r.state];
  return says ? noRuns("clock", "No steps yet", says) : noRuns("ban", "No steps", "The run ended before it started" + (r.reason ? ": " + r.reason : "") + ".");
}

// stepLog is the card of a step's log (Log/Line): its name, how long it ran and how it exited, Live while it's read
// from the running pod, and Raw, which opens it as text; each line with its time since the step started.
function stepLog(path, r, s, logs) {
  const log = Array.isArray(logs) ? logs.find((l) => l.container === s.name) : null;
  const ended = s.state === "failed" ? [s.reason || null, "exited " + s.exitCode].filter(Boolean).join(" · ") : null;
  const raw = config.restUrl + "/v1" + path + "/runs/" + r.number + "/logs/" + encodeURIComponent(s.name);
  return h("section", { class: "card" },
    h("div", { class: "card-header" }, h("h2", { class: "card-title" }, s.name),
      s.startedAt || ended ? h("span", { class: "card-meta" }, [s.startedAt ? duration(s) : null, ended].filter(Boolean).join(" · ")) : null,
      log && log.live ? h("span", { class: "badge-quiet tone-info" }, "Live") : null,
      log && log.text ? h("a", { class: "btn btn-ghost btn-sm", href: raw, target: "_blank", rel: "noopener", title: "Open " + s.name + "'s log as text" }, "Raw") : null),
    h("div", { class: "log" }, log && log.text ? logLines(log, s) : h("p", { class: "log-note" }, noLog(r, s, logs, log))));
}

// noLog says why a step shows no log.
function noLog(r, s, logs, log) {
  if (logs instanceof Error) return "The logs couldn't be read: " + logs.message;
  if (log) return log.live ? "Nothing logged yet." : "It logged nothing.";
  if (s.state === "waiting") return active.includes(r.state) ? "It hasn't started yet." : "It never started.";
  if (s.state === "running") return "Nothing logged yet.";
  return logs.length ? "No log was kept for this step." : "This installation keeps no logs: it has no object store for them.";
}

// logLines are a log's lines, each with how long after its step started it was logged: m:ss, or h:mm:ss for every
// line of a step that ran past an hour; a log kept before logs kept their times has none.
function logLines(log, s) {
  const lines = log.text.replace(/\n$/, "").split("\n");
  const times = log.times || [];
  const from = new Date(s.startedAt || times[0] || 0).getTime();
  const seconds = times.map((t) => Math.max(0, Math.floor((new Date(t).getTime() - from) / 1000)));
  const hours = seconds.some((n) => n >= 3600);
  return lines.map((text, i) => h("div", { class: "log-line" },
    h("span", { class: "log-time", title: times[i] ? whenExactly(times[i]) : null }, i < seconds.length ? clock(seconds[i], hours) : ""), h("span", { class: "log-text" }, text)));
}

// clock is a number of seconds as a log's line says it: m:ss, or h:mm:ss.
function clock(n, hours) {
  const two = (k) => String(k).padStart(2, "0");
  return hours ? Math.floor(n / 3600) + ":" + two(Math.floor(n / 60) % 60) + ":" + two(n % 60) : Math.floor(n / 60) + ":" + two(n % 60);
}

// definition is the card of a run's job as /cobranch.yaml declared it when the run was queued, its lines numbered.
function definition(r) {
  const lines = jobYaml(r.definition);
  return h("section", { class: "card" },
    h("div", { class: "card-header" }, h("h2", { class: "card-title" }, "Definition"), h("span", { class: "card-meta" }, "/cobranch.yaml · " + r.definition.name)),
    h("div", { class: "log" }, lines.map((text, i) => h("div", { class: "log-line" }, h("span", { class: "log-time" }, String(i + 1)), h("span", { class: "log-text" }, text)))));
}

// downloadButton is Download logs: every step's log, read when pressed, in one text file, each under its name.
function downloadButton(runPath, r, fail) {
  const button = h("button", { class: "btn", type: "button", title: "Download every step's log in one file" }, icon("file-code"), "Download logs");
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      const { logs } = await api("GET", runPath + "/logs");
      const text = logs.map((l) => "==> " + l.container + " <==\n" + l.text + (l.text.endsWith("\n") || !l.text ? "" : "\n")).join("\n");
      const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
      h("a", { href: url, download: r.job.displayName.replaceAll("/", "-") + "-" + r.number + ".log" }).click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (err) {
      fail(err);
    } finally {
      button.disabled = false;
    }
  });
  return button;
}

// follow reads a run that hasn't ended again every five seconds while its page shows: the page draws again whole once
// its state changes, and while it runs, its line, its steps and their logs draw again from what it reads.
function follow(runPath, view, header, draw) {
  const stop = new AbortController();
  follows(stop);
  (async () => {
    while (!stop.signal.aborted) {
      await new Promise((resolve) => setTimeout(resolve, 5000));
      if (stop.signal.aborted || !header.isConnected) return;
      try {
        const now = await api("GET", runPath, undefined, {}, stop.signal);
        if (now.state !== view.r.state) return void render();
        if (now.state !== "running" || now.definition.kind === "webhook") continue;
        const [steps, logs] = await Promise.all([api("GET", runPath + "/steps", undefined, {}, stop.signal).then((d) => d.steps),
          api("GET", runPath + "/logs", undefined, {}, stop.signal).then((d) => d.logs)]);
        if (!header.isConnected) return;
        Object.assign(view, { r: now, steps, logs });
        draw();
      } catch {
        if (stop.signal.aborted) return;
        // A read that fails leaves the page as it was, and the next one tries again.
      }
    }
  })();
}

// jobYaml is a job's definition as /cobranch.yaml declares it, a line each, under its name: its triggers, timeout,
// permissions and secrets, then its work: a pod spec, a script and its image, or a webhook.
export function jobYaml(d) {
  const job = { on: d.on };
  if (d.timeout) job.timeout = d.timeout.replace(/(\d)m0s$/, "$1m").replace(/(\d)h0m$/, "$1h");
  job.permissions = d.permissions;
  for (const k of ["secrets", "pod", "script", "image", "webhook"]) if (d[k]) job[k] = d[k];
  if (!d.checkout && d.kind !== "webhook") job.checkout = false;
  // The job's own keys keep the order above; yamlLines orders those of the objects in it.
  return [d.name + ":", ...Object.entries(job).flatMap(([k, x]) => yamlEntry("  " + k + ":", x, "    "))];
}

// yamlLines writes a JSON object or array as YAML's block style, a line each, indented by pad: a short list of
// scalars in flow style, a string of lines as a literal block, and a scalar quoted only where YAML would read it as
// something else.
function yamlLines(v, pad) {
  if (Array.isArray(v)) {
    return v.flatMap((x) => {
      if (x && typeof x === "object" && !Array.isArray(x) && Object.keys(x).length) {
        const [first, ...more] = yamlLines(x, pad + "  ");
        return [pad + "- " + first.trimStart(), ...more];
      }
      return yamlEntry(pad + "-", x, pad + "  ");
    });
  }
  // The database keeps an object's keys in an order of its own: a pod spec reads best with each container's name and
  // image first and its init containers before its containers, as such files are written.
  const keys = Object.keys(v).sort((a, b) => keyRank(a) - keyRank(b));
  return keys.map((k) => [k, v[k]]).flatMap(([k, x]) => yamlEntry(pad + (/^[A-Za-z0-9_][A-Za-z0-9_./-]*$/.test(k) ? k : JSON.stringify(k)) + ":", x, pad + "  "));
}

// keyRank orders an object's keys as yamlLines writes them: those firstKeys names in its order, then the rest as they
// come.
const firstKeys = ["name", "image", "initContainers", "containers"];
const keyRank = (k) => (firstKeys.includes(k) ? firstKeys.indexOf(k) : firstKeys.length);

// yamlEntry is a value after its key or its dash (head): on the same line when it's a scalar, an empty or a short
// list, else under it, indented by pad.
function yamlEntry(head, x, pad) {
  if (typeof x === "string" && x.includes("\n") && !/^[ \t]/.test(x)) {
    const chomp = x.endsWith("\n\n") ? "+" : x.endsWith("\n") ? "" : "-";
    return [head + " |" + chomp, ...x.replace(/\n$/, "").split("\n").map((l) => (l ? pad + l : ""))];
  }
  if (x === null || typeof x !== "object") return [head + " " + yamlScalar(x)];
  if (Array.isArray(x) ? !x.length : !Object.keys(x).length) return [head + (Array.isArray(x) ? " []" : " {}")];
  if (Array.isArray(x) && x.every((e) => e === null || typeof e !== "object") && JSON.stringify(x).length <= 60) return [head + " [" + x.map(yamlScalar).join(", ") + "]"];
  return [head, ...yamlLines(x, pad)];
}

// yamlScalar is a scalar as YAML writes it: plain unless YAML would read it otherwise (a number, a boolean, null,
// an indicator, a comment or a mapping), then quoted as JSON is.
function yamlScalar(x) {
  if (x === null || x === undefined) return "null";
  if (typeof x !== "string") return String(x);
  const quoted = x === "" || /^([\s,[\]{}#&*!|>'"%@`]|[-?:](\s|$))|\s$|:(\s|$)|\s#|[,[\]{}\n\t]/.test(x) ||
    /^(true|false|yes|no|on|off|y|n|null|~|[-+]?(\d[\d_]*(\.\d*)?|\.\d+)([eE][-+]?\d+)?|0x[\da-fA-F]+|0o[0-7]+)$/i.test(x);
  return quoted ? JSON.stringify(x) : x;
}

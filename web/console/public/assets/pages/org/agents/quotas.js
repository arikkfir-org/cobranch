import { agentPage, card, emptyState, micros, providerName, spendReport, taskPage, tokens, usd } from "../../../lib/agent.js";
import { h } from "../../../lib/dom.js";
import { plural } from "../../../lib/format.js";
import { avatar } from "../../../lib/icons.js";

export const tab = { label: "Quotas", order: 20 };
export const view = "organization.agents-quotas";
export default ({ org, main }) => showQuotas(org, main);

// stat is one of the month's figures: its label, its value, and a line on what makes it up.
const stat = (label, value, meta) => h("div", { class: "stat" }, h("span", { class: "stat-label" }, label), h("span", { class: "stat-value" }, value),
  h("span", { class: "stat-meta", title: meta }, meta));

// showQuotas is what the organization's agents spent this month, in the viewer's time zone (Agents · Quotas): the
// month's figures, then each agent's tasks and spend with its costliest task against its cap, and each model's tasks,
// tokens and cost. The counts and sums are the organization's, tasks the viewer can't see included; the costliest task
// is named only when the viewer sees it.
async function showQuotas(org, main) {
  const report = await spendReport(org);
  const month = new Date(report.since).toLocaleDateString(undefined, { month: "long", timeZone: report.zone });
  const spent = report.agents.reduce((sum, a) => sum + micros(a.spent), 0);
  const tasks = report.agents.reduce((sum, a) => sum + a.tasks, 0);
  const failed = report.agents.reduce((sum, a) => sum + a.failedOnCap, 0);
  const active = report.agents.reduce((sum, a) => sum + a.active, 0);
  const top = report.agents.filter((a) => a.costliest).sort((x, y) => micros(y.costliest.spent) - micros(x.costliest.spent))[0];
  const highest = top ? micros(top.costliest.spent) : 0;
  main.replaceChildren(
    h("div", { class: "stats" },
      stat("Spent this month", usd(spent), month + ", so far · " + plural(report.agents.length, "agent")),
      stat("Tasks", String(tasks), (tasks - failed - active) + " finished · " + failed + " failed on their cap · " + active + " active"),
      top ? stat("Highest task", usd(highest), [top.cap ? "of a $" + top.cap + " cap" : "uncapped", "#" + top.costliest.number + " " + top.costliest.title].join(" · "))
        : stat("Highest task", "None yet", "No task you see spent anything this month."),
      stat("Monthly limit", "None", "A task stops only at its agent's cap.")),
    card("By agent", "this month", report.agents.length ? agentTable(org, report, highest) : emptyState("bot", "No agents yet", "Each agent's tasks and spend show here.")),
    card("By model", "this month", report.models.length ? modelTable(report) : emptyState("sparkles", "No models yet",
      "The organization's models, and any other its tasks call, show here with what their calls cost.")));
}

// costliestMeter is an agent's costliest task this month against its cap (Meter): warning from 80% of it, danger once
// reached, which fails the task; an uncapped agent's against the month's costliest task.
function costliestMeter(org, a, highest) {
  if (!a.costliest) return h("span", { class: "cell-muted" }, a.tasks ? "None you see" : "–");
  const cost = micros(a.costliest.spent);
  const cap = a.cap ? micros(a.cap) : 0;
  const share = cap ? cost / cap : highest ? cost / highest : 0;
  const tone = !cap ? "" : share >= 1 ? " meter-danger" : share >= 0.8 ? " meter-warning" : "";
  const said = !cap ? usd(cost) : share >= 1 ? usd(cost) + " · cap reached, task failed" : usd(cost) + " · " + Math.round(share * 100) + "%";
  return h("a", { class: "quota" + tone, href: taskPage(org, a.costliest), "data-nav": "", title: "#" + a.costliest.number + " " + a.costliest.title },
    h("span", { class: "meter-track" }, h("span", { class: "meter-bar", style: "width: " + Math.min(100, share * 100).toFixed(1) + "%" })),
    h("span", { class: "quota-said" }, said));
}

// agentTable is each agent's month: its tasks, what they spent in all and each on average, its cap, and its costliest.
function agentTable(org, report, highest) {
  const row = (a) => h("tr", {},
    h("td", {}, h("a", { class: "quota-agent", href: agentPage(org, a.agent.id), "data-nav": "" }, avatar(a.agent.displayName, true, 24),
      h("span", { class: "quota-agent-text" }, h("span", { class: "quota-agent-name" }, a.agent.displayName), h("code", {}, a.model || "No model")))),
    h("td", {}, String(a.tasks)),
    h("td", { class: "cell-mono" }, usd(micros(a.spent))),
    h("td", { class: "cell-mono" }, a.tasks ? usd(micros(a.spent) / a.tasks) : "–"),
    h("td", { class: "cell-mono" }, a.cap ? "$" + a.cap : "None"),
    h("td", { class: "quota-cell" }, costliestMeter(org, a, highest)));
  return h("table", { class: "table quota-table" }, h("thead", {}, h("tr", {}, h("th", {}, "Agent"), h("th", {}, "Tasks"), h("th", {}, "Spent"),
    h("th", {}, "Per task"), h("th", {}, "Cap per task"), h("th", {}, "Highest task against its cap"))), h("tbody", {}, report.agents.map(row)));
}

// modelTable is each model's month: the tasks that called it, the tokens it read and wrote, and what its calls cost.
function modelTable(report) {
  const row = (m) => h("tr", {}, h("td", { class: "cell-mono" }, m.model), h("td", {}, providerName(m.provider)), h("td", {}, String(m.tasks)),
    h("td", { class: "cell-mono" }, tokens(m.inputTokens)), h("td", { class: "cell-mono" }, tokens(m.outputTokens)), h("td", { class: "cell-mono" }, usd(micros(m.spent))));
  return h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Model"), h("th", {}, "Provider"), h("th", {}, "Tasks"),
    h("th", {}, "Input tokens"), h("th", {}, "Output tokens"), h("th", {}, "Spent"))), h("tbody", {}, report.models.map(row)));
}

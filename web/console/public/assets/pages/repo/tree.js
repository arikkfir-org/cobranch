import { branchSubject } from "../../lib/chat.js";
import { filesBranch, repositoryFiles } from "../../lib/code.js";

export const nav = { label: "Files", order: 10 };
export const view = "repository.code";
export const crumbs = () => [{ label: "Files" }];
// subject is the branch's conversation, on its files.
export const subject = async ({ org, repo, path, at, rest, can }) => {
  const branch = await filesBranch(path, rest);
  return branch ? branchSubject(org, repo, path, at, branch, can) : null;
};

// A directory at a ref: tree/ then the ref and the directory's path, main's root without them.
export default ({ org, repo, path, at, rest, can }) => repositoryFiles(org, repo, path, at, false, rest, can);

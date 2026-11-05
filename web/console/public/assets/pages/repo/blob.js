import { branchSubject } from "../../lib/chat.js";
import { filesBranch, repositoryFiles } from "../../lib/code.js";

export const under = "tree";
export const accepts = (rest) => rest.length >= 2;
export const view = "repository.file";
export const crumbs = ({ root, rest }) => [{ label: "Files", href: root + "/tree" }, { label: rest.join("/") }];
// subject is the branch's conversation, on its files.
export const subject = async ({ org, repo, path, at, rest, can }) => {
  const branch = await filesBranch(path, rest);
  return branch ? branchSubject(org, repo, path, at, branch, can) : null;
};

// A file at a ref: blob/ then the ref and the file's path.
export default ({ org, repo, path, at, rest, can }) => repositoryFiles(org, repo, path, at, true, rest, can);

import { repositoryFiles } from "../../lib/code.js";

export const under = "";
export const view = "repository.code";

// A directory at a ref: tree/ then the ref and the directory's path.
export default ({ org, repo, path, at, rest, can }) => repositoryFiles(org, repo, path, at, false, rest, can);

import { repositoryFiles } from "../../lib/code.js";

export const under = "";
export const accepts = (rest) => rest.length >= 2;
export const view = "repository.file";

// A file at a ref: blob/ then the ref and the file's path.
export default ({ org, repo, path, at, rest, can }) => repositoryFiles(org, repo, path, at, true, rest, can);

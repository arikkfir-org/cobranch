// What every page of the Console reads: where it runs, who views it and what the page shown holds. Other modules read
// these bindings, which stay live, and change them only through the setters beside them.

// The Console's root, wherever the operator mounts it (its route strips the prefix): the path of the Console's URL,
// which config.json names, and which every directory serves. Every page's path is relative to it; the Auth
// server sends a failed sign-in back to /sign-in.
export let base = "/";
export let config = { restUrl: "", authUrl: "", gitUrl: "", gitSshUrl: "" };
export let me = null;
export let orgs = [];
export let roles = []; // the roles the organization shown grants
export let permissions = []; // what a role at an organization may hold
let chatPoll = null; // stops the chat the page follows, once the Console draws another
// follows makes stop the one way to stop the chat the page follows, stopping the one before: null when it follows
// none.
export const follows = (stop) => { if (chatPoll) chatPoll.abort(); chatPoll = stop; };
export let carried = null; // what the next repository page says went wrong, once: a step that failed after the step before it re-rendered

export const setBase = (b) => { base = b; };
export const setConfig = (c) => { config = c; };
export const setMe = (m) => { me = m; };
export const setOrgs = (o) => { orgs = o; };
export const setRoles = (r) => { roles = r; };
export const setPermissions = (p) => { permissions = p; };
export const setCarried = (err) => { carried = err; };

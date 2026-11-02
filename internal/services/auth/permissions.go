package auth

// The permission catalog of the principals design, by the scope a permission is granted at. A role names permissions
// from it alone; the built-in roles come with migration 2, and the tests hold them to this list.
var (
	InstallationPermissions = []string{"installation.manage", "installation.users.manage", "orgs.create"}
	OrganizationPermissions = []string{
		"org.read", "org.manage", "org.members.manage", "org.grants.manage", "org.principals.manage", "org.audit.read",
		"agents.create", "agents.assign", "repos.create", "issues.read", "issues.write", "issues.manage",
	}
	RepositoryPermissions = []string{
		"repo.read", "repo.push", "repo.merge", "repo.bypass", "repo.manage",
		"annotations.write", "annotations.delete_any", "reviews.submit", "messages.post", "messages.moderate", "jobs.run",
		"secrets.manage", "secrets.read",
	}
)

// Permissions is every permission of the catalog.
func Permissions() []string {
	all := make([]string, 0, len(InstallationPermissions)+len(OrganizationPermissions)+len(RepositoryPermissions))
	all = append(all, InstallationPermissions...)
	all = append(all, OrganizationPermissions...)
	return append(all, RepositoryPermissions...)
}

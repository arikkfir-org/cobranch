package graphql

import (
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/repos"
)

// quota is q as the API shows it.
func quota(q limits.Quota) *Usage {
	return &Usage{Used: q.Used, Limit: q.Limit}
}

// organizationUsage is u as the API shows it.
func organizationUsage(u repos.Usage) *OrganizationUsage {
	return &OrganizationUsage{Repositories: quota(u.Repositories), GitData: quota(u.GitData),
		ByRepository: mapAll(u.ByRepository, func(r repos.RepositoryUsage) *RepositoryUsage {
			return &RepositoryUsage{Repository: repository(r.Repository), GitData: r.GitData}
		})}
}

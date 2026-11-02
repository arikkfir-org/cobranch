package agents

import (
	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// The organization's built-in AI reviewer, which reviews every push of a branch
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI reviewer").
const (
	// PurposeReview marks the AI reviewer among the organization's agents: Cobranch makes one with every organization.
	PurposeReview = "review"
	// KindReview is the kind of a task Cobranch starts for the AI reviewer: a review of one head of a branch.
	KindReview = "review"
	// ReviewerName is what Cobranch calls the AI reviewer it makes; an admin may rename it.
	ReviewerName = "AI reviewer"
)

// Reviewer is the AI reviewer Cobranch makes with an organization, as id: a built-in agent holding Automation at the
// organization, so it reads, annotates, reviews and posts in each of its repositories, and nothing more. It reviews
// nothing until an admin picks its model.
func Reviewer(id, orgID uuid.UUID) Agent {
	return Agent{ID: id, OrgID: orgID, Name: ReviewerName, BuiltIn: true, Purpose: PurposeReview, Version: 1,
		Description: "Reviews every push of a branch, as main's /cobranch-review.yaml says, once it has a model.",
		Grants:      []Grant{{RoleID: auth.RoleOrgAutomation, RoleName: "Automation"}}}
}

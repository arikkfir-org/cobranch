package graphql

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/orgs"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	Branch(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string) (code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, q code.HistoryQuery) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	SetAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error)
	DeleteAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha, name string, version int64) error
	Review(ctx context.Context, v code.Viewer, repoID, id uuid.UUID) (code.Review, error)
	SubmitReview(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string, in code.ReviewInput) (code.Review, error)
	RequestReview(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch string) (code.AIReview, error)
	Merge(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string, in code.MergeInput) (code.Merged, error)
	DeleteBranch(ctx context.Context, v code.Viewer, repoID uuid.UUID, name, head string) error
	DeletedBranch(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string) (code.DeletedBranch, error)
	Tags(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Tag, error)
	Tree(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Tree, error)
	TreeCommits(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.TreeCommits, error)
	Paths(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref string) (code.Paths, error)
	Blame(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.FileBlame, error)
	File(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.File, error)
	Runs(ctx context.Context, v code.Viewer, repoID uuid.UUID, q code.RunsQuery) (code.RunPage, error)
	Run(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Run, error)
	RunLogs(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) ([]code.RunLog, error)
	RunSteps(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) ([]jobs.Step, error)
	CancelRun(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Run, error)
	StartRun(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Run, error)
	Environments(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch string) ([]code.Environment, error)
	Environment(ctx context.Context, v code.Viewer, repoID, id uuid.UUID) (code.Environment, error)
	Deliveries(ctx context.Context, v code.Viewer, repoID, environmentID uuid.UUID, before int64, limit int) (code.DeliveryPage, error)
	Delivery(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Delivery, error)
	Redeliver(ctx context.Context, v code.Viewer, repoID, environmentID uuid.UUID) (code.Delivery, error)
	PauseEnvironment(ctx context.Context, v code.Viewer, repoID, environmentID uuid.UUID) (code.Environment, error)
	ResumeEnvironment(ctx context.Context, v code.Viewer, repoID, environmentID uuid.UUID) (code.Environment, error)
	StartDelivery(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Delivery, error)
	BranchDiff(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch string) (code.Diff, error)
	BranchDiffCounts(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch string) (code.DiffCounts, error)
	CommitDiff(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Diff, error)
	Viewed(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch string) ([]code.ViewedFile, error)
	UpdateViewed(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, tag string, changes []code.ViewedChange) ([]code.ViewedFile, error)
	RerunRun(ctx context.Context, v code.Viewer, repoID uuid.UUID, number int64) (code.Run, error)
	RunJob(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, job string) (code.Run, error)
	PostEvent(ctx context.Context, v code.Viewer, repoID uuid.UUID, in code.EventInput) ([]code.Run, error)
	Attention(ctx context.Context, v code.Viewer, orgID uuid.UUID) ([]code.Attention, error)
	Activity(ctx context.Context, v code.Viewer, orgID uuid.UUID, before code.ActivityCursor, limit int) (code.ActivityPage, error)
}

// unavailable is the code of the error a field answers when the installation lacks what it needs: a Git server.
const unavailable = "UNAVAILABLE"

// misses are the reasons of the NOT_FOUND errors of what a ref or a path didn't find.
var misses = map[error]string{
	code.ErrRefNotFound:  "ref.not_found",
	code.ErrPathNotFound: "path.not_found",
	code.ErrNotTree:      "path.file",
	code.ErrNotBlob:      "path.directory",
	code.ErrSubmodule:    "path.submodule",
}

// codeFail is fail for the code's reads: NOT_FOUND with the reason of what a ref or a path didn't find, and
// UNAVAILABLE without a Git server.
func codeFail(ctx context.Context, err error, what string) error {
	for miss, reason := range misses {
		if errors.Is(err, miss) {
			return refuse(ctx, notFound, reason, miss.Error())
		}
	}
	if errors.Is(err, code.ErrUnavailable) {
		return refuse(ctx, unavailable, "git.unavailable", code.ErrUnavailable.Error())
	}
	return fail(ctx, err, "repository", what)
}

// treeCommits is what last changed a directory as the schema shows it: each entry's latest commit by the entry's
// name.
func treeCommits(last code.TreeCommits) *TreeCommits {
	commits := make(map[string]*Commit, len(last.Commits))
	for _, c := range last.Commits {
		commits[c.SHA] = commit(c)
	}
	out := &TreeCommits{Commit: last.Commit, Path: last.Path, Count: last.Count, Latest: commits[last.Latest], Entries: []*EntryCommit{}}
	for _, name := range slices.Sorted(maps.Keys(last.Entries)) {
		out.Entries = append(out.Entries, &EntryCommit{Name: name, Commit: commits[last.Entries[name]]})
	}
	return out
}

// blameOf is a file's blame as the schema shows it: each run with its commit, when a push recorded it.
func blameOf(blame code.FileBlame) *Blame {
	commits := make(map[string]*Commit, len(blame.Commits))
	for _, c := range blame.Commits {
		commits[c.SHA] = commit(c)
	}
	out := &Blame{Commit: blame.Commit, Path: blame.Path, Ranges: make([]*BlameRange, 0, len(blame.Ranges))}
	for _, r := range blame.Ranges {
		out.Ranges = append(out.Ranges, &BlameRange{Start: r.Start, Lines: r.Lines, Sha: r.SHA, Commit: commits[r.SHA]})
	}
	return out
}

// historyParents are the parents a history follows as the domain names them.
var historyParents = map[HistoryParents]code.Parents{HistoryParentsFirst: code.ParentsFirst, HistoryParentsAll: code.ParentsAll}

// entryKinds are the kinds of a tree's entries as the API shows them.
var entryKinds = map[code.EntryKind]EntryKind{
	code.KindTree: EntryKindTree, code.KindBlob: EntryKindBlob, code.KindSymlink: EntryKindSymlink, code.KindSubmodule: EntryKindSubmodule,
}

func entry(e code.Entry) *TreeEntry {
	out := &TreeEntry{Name: e.Name, Path: e.Path, Kind: entryKinds[e.Kind], Mode: e.Mode, Sha: e.SHA}
	if e.Kind == code.KindBlob || e.Kind == code.KindSymlink {
		size := int(e.Size)
		out.Size = &size
	}
	return out
}

func file(f code.File) *File {
	out := &File{Commit: f.Commit, Path: f.Path, Name: f.Name, Kind: entryKinds[f.Kind], Mode: f.Mode, Sha: f.SHA, Size: int(f.Size),
		Binary: f.Binary, Truncated: f.Truncated}
	if !f.Binary {
		out.Text = &f.Text
	}
	if f.HTML != "" {
		out.HTML = &f.HTML
	}
	return out
}

// fileStatuses and lineKinds map the domain's diffs' statuses and kinds to the schema's.
var (
	fileStatuses = map[code.FileStatus]FileStatus{
		code.FileAdded: FileStatusAdded, code.FileModified: FileStatusModified, code.FileDeleted: FileStatusDeleted,
		code.FileRenamed: FileStatusRenamed, code.FileTypeChanged: FileStatusTypeChanged,
	}
	lineKinds = map[code.LineKind]LineKind{code.LineContext: LineKindContext, code.LineAdded: LineKindAdded, code.LineRemoved: LineKindRemoved}
)

// diff is d as the API shows it.
func diff(d code.Diff) *Diff {
	return &Diff{Base: optional(d.Base), BaseTip: optional(d.BaseTip), Head: d.Head, Files: mapAll(d.Files, fileDiff),
		FilesLeftOut: d.FilesLeftOut, Additions: d.Additions, Deletions: d.Deletions}
}

func fileDiff(f code.FileDiff) *FileDiff {
	return &FileDiff{Status: fileStatuses[f.Status], Path: f.Path, OldPath: optional(f.OldPath), OldMode: optional(f.OldMode),
		Mode: optional(f.Mode), OldObject: optional(f.OldObject), Object: optional(f.Object), Binary: f.Binary, Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated,
		Hunks: mapAll(f.Hunks, hunk)}
}

func hunk(h code.Hunk) *Hunk {
	return &Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section,
		Lines: mapAll(h.Lines, diffLine)}
}

func diffLine(l code.DiffLine) *DiffLine {
	out := &DiffLine{Kind: lineKinds[l.Kind], Text: l.Text, NoNewline: l.NoNewline}
	if l.Kind != code.LineAdded {
		out.Old = &l.Old
	}
	if l.Kind != code.LineRemoved {
		out.New = &l.New
	}
	return out
}

// optional is s, or nil when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func commitPrincipal(p *code.Principal) *CommitPrincipal {
	if p == nil {
		return nil
	}
	return &CommitPrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

// actor is who did something, with their kind, as the API shows them; nil for nobody.
func actor(a *code.Actor) *Actor {
	if a == nil {
		return nil
	}
	return &Actor{ID: a.ID.String(), DisplayName: a.DisplayName, Kind: a.Kind}
}

func person(p code.Person) *Person {
	return &Person{Name: p.Name, Email: p.Email, At: p.At.UTC(), Zone: p.Zone, Principal: commitPrincipal(p.Principal)}
}

// commit is c as the API shows it; a list's commits come without their pushes.
func commit(c code.Commit) *Commit {
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return &Commit{Sha: c.SHA, Tree: c.Tree, Parents: parents, Message: c.Message, Author: person(c.Author), Committer: person(c.Committer),
		Signature: c.Signature, Annotations: mapAll(c.Annotations, annotation), Reviews: mapAll(c.Reviews, review)}
}

// reviewStates maps the domain's review states to the schema's.
var reviewStates = map[code.ReviewState]ReviewState{
	code.ReviewApproved: ReviewStateApproved, code.ReviewChangesRequested: ReviewStateChangesRequested, code.ReviewNeutral: ReviewStateNeutral,
}

// reviewInput is the review in submits, as the code service takes it.
func reviewInput(in ReviewInput) code.ReviewInput {
	out := code.ReviewInput{State: reviewStateOf(in.State), Body: in.Body, Comments: make([]code.ReviewComment, len(in.Comments))}
	if in.Branch != nil {
		out.Branch = *in.Branch
	}
	for i, c := range in.Comments {
		out.Comments[i] = code.ReviewComment{Anchor: *anchorOf(c.Anchor), Body: c.Body}
	}
	return out
}

// reviewStateOf is the domain's review state that state is.
func reviewStateOf(state ReviewState) code.ReviewState {
	for domain, schema := range reviewStates {
		if schema == state {
			return domain
		}
	}
	return ""
}

func review(r code.Review) *Review {
	return &Review{ID: r.ID.String(), Sha: r.SHA, State: reviewStates[r.State], Body: r.Body, HTML: r.HTML, Reviewer: commitPrincipal(r.Reviewer),
		Person: r.Person, SubmittedAt: r.SubmittedAt.UTC()}
}

// annotationStatuses maps the domain's annotation statuses to the schema's, and back.
var annotationStatuses = map[code.AnnotationStatus]AnnotationStatus{
	code.AnnotationPending: AnnotationStatusPending, code.AnnotationInProgress: AnnotationStatusInProgress,
	code.AnnotationFailed: AnnotationStatusFailed, code.AnnotationSuccessful: AnnotationStatusSuccessful,
}

func annotation(a code.Annotation) *Annotation {
	out := &Annotation{ID: a.ID.String(), Name: a.Name, Status: annotationStatuses[a.Status], Summary: a.Summary, Link: a.Link,
		Principal: commitPrincipal(a.Principal), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), StartedAt: a.StartedAt.UTC(),
		Version: int(a.Version)}
	if a.CompletedAt != nil {
		at := a.CompletedAt.UTC()
		out.CompletedAt = &at
	}
	return out
}

// annotationInput is in as the domain takes it, for the annotation of name.
func annotationInput(name string, in AnnotationInput) code.AnnotationInput {
	out := code.AnnotationInput{Name: name, Summary: in.Summary, Link: in.Link}
	for domain, schema := range annotationStatuses {
		if schema == in.Status {
			out.Status = domain
		}
	}
	return out
}

// expected is the version of an annotation that expectedVersion names, from 1: 0 without one.
func expected(expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return 0, nil
	}
	if *expectedVersion < 1 {
		return 0, &orgs.ValidationError{Field: "expectedVersion", Code: "version.invalid", Message: "a version of the annotation, from 1"}
	}
	return int64(*expectedVersion), nil
}

// codeRepo is the viewer and the repository a mutation of its code names.
func codeRepo(ctx context.Context, repositoryID string) (code.Viewer, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	repoID, err := parseID(ctx, repositoryID, "repository")
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	return v.User, repoID, nil
}

// Branch is one of a repository's live branches as the schema shows it, with the organization and repository it is
// of, from which its issue and environments resolve.
type Branch struct {
	Name          string             `json:"name"`
	Head          string             `json:"head"`
	Commit        *Commit            `json:"commit,omitempty"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
	Base          *string            `json:"base,omitempty"`
	Status        *BranchStatus      `json:"status,omitempty"`
	Checks        []*BranchCheck     `json:"checks"`
	Ahead         *int               `json:"ahead,omitempty"`
	Behind        *int               `json:"behind,omitempty"`
	Requirements  *MergeRequirements `json:"requirements,omitempty"`
	Merge         *BranchMerge       `json:"merge,omitempty"`
	SettingsError *string            `json:"settingsError,omitempty"`
	AIReview      *AIReview          `json:"aiReview,omitempty"`
	ManualJobs    []string           `json:"manualJobs"`

	orgID, repoID uuid.UUID
}

// of makes b the branch of repo, which its issue and environments resolve from.
func (b *Branch) of(ctx context.Context, repo *Repository) error {
	var err error
	if b.orgID, err = parseID(ctx, repo.OrganizationID, "organization"); err != nil {
		return err
	}
	b.repoID, err = parseID(ctx, repo.ID, "repository")
	return err
}

// branch is b as the API shows it: every branch but main with its base, status, checks and facts, any branch
// /cobranch.yaml names with what a merge into it needs, and any branch merged with its last merge.
func branch(b code.Branch) *Branch {
	out := &Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Checks: mapAll(b.Checks, check)}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	if b.Base != "" {
		status := branchStatuses[b.Status]
		out.Base, out.Status = &b.Base, &status
	}
	if b.Facts != nil {
		out.Ahead, out.Behind = &b.Facts.Ahead, &b.Facts.Behind
	}
	if r := b.Requirements; r != nil {
		annotations := r.Annotations
		if annotations == nil {
			annotations = []string{}
		}
		out.Requirements = &MergeRequirements{Annotations: annotations, LinearHistory: r.LinearHistory}
		if r.Reviews != nil {
			out.Requirements.Reviews = &ReviewRequirements{Approvals: r.Reviews.Approvals, Ai: r.Reviews.AI}
		}
	}
	if m := b.Merge; m != nil {
		out.Merge = &BranchMerge{Head: m.Head, By: commitPrincipal(m.By), At: m.At.UTC()}
	}
	if b.SettingsError != "" {
		out.SettingsError = &b.SettingsError
	}
	out.ManualJobs = append([]string{}, b.ManualJobs...)
	if b.AIReview != nil {
		out.AIReview = aiReview(*b.AIReview)
	}
	return out
}

// aiReview is the AI reviewer's task for a branch's head as the API shows it.
func aiReview(r code.AIReview) *AIReview {
	out := &AIReview{Task: int(r.Task), State: AIReviewState(strings.ToUpper(string(r.State)))}
	if r.Reason != "" {
		out.Reason = &r.Reason
	}
	return out
}

// deletedBranch is b as the API shows it.
func deletedBranch(b code.DeletedBranch) *DeletedBranch {
	out := &DeletedBranch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt.UTC(), DeletedAt: b.DeletedAt.UTC(), DeletedBy: commitPrincipal(b.DeletedBy)}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	if m := b.Merge; m != nil {
		out.Merge = &BranchMerge{Head: m.Head, By: commitPrincipal(m.By), At: m.At.UTC()}
	}
	return out
}

// branchStatuses maps the domain's branch statuses to the schema's.
var branchStatuses = map[code.BranchStatus]BranchStatus{
	code.StatusProcessing: BranchStatusProcessing, code.StatusBlocked: BranchStatusBlocked, code.StatusEmpty: BranchStatusEmpty,
	code.StatusMerged: BranchStatusMerged, code.StatusReady: BranchStatusReady,
}

// checkKinds maps the domain's kinds of checks to the schema's.
var checkKinds = map[code.CheckKind]BranchCheckKind{
	code.CheckAnnotation: BranchCheckKindAnnotation, code.CheckLinearHistory: BranchCheckKindLinearHistory, code.CheckApprovals: BranchCheckKindApprovals,
	code.CheckChangesRequested: BranchCheckKindChangesRequested, code.CheckAI: BranchCheckKindAi, code.CheckUpToDate: BranchCheckKindUpToDate,
}

// check is one of a branch's checks as the API shows it, each field only where its kind has it.
func check(c code.Check) *BranchCheck {
	out := &BranchCheck{Kind: checkKinds[c.Kind], Met: c.Met}
	switch c.Kind {
	case code.CheckAnnotation:
		out.Name = &c.Name
		if c.Status != "" {
			status := annotationStatuses[c.Status]
			out.Status = &status
		}
	case code.CheckChangesRequested:
		out.Name = &c.Name
	case code.CheckApprovals:
		out.Count, out.Required = &c.Count, &c.Required
	case code.CheckAI:
		state := AICheckState(strings.ToUpper(string(c.AI)))
		out.Ai = &state
		if c.Reason != "" {
			out.Reason = &c.Reason
		}
	default:
		out.Count = &c.Count
	}
	return out
}

// mergeMethods maps the schema's merge methods to the domain's.
var mergeMethods = map[MergeMethod]code.MergeMethod{MergeMethodMerge: code.MethodMerge, MergeMethodFastForward: code.MethodFastForward}

// mergeInput is in as the domain takes it; nil asks for the defaults.
func mergeInput(in *MergeInput) code.MergeInput {
	var out code.MergeInput
	if in == nil {
		return out
	}
	if in.Head != nil {
		out.Head = *in.Head
	}
	if in.Method != nil {
		out.Method = mergeMethods[*in.Method]
	}
	return out
}

// mergeFail is fail for merges: CONFLICT for a branch that moved and a merge Cobranch won't make, and UNAVAILABLE
// without a Git server.
func mergeFail(ctx context.Context, err error) error {
	var refused *code.MergeError
	switch {
	case errors.Is(err, code.ErrBranchMoved):
		return refuse(ctx, conflict, "branch.moved", code.ErrBranchMoved.Error())
	case errors.As(err, &refused):
		return refuse(ctx, conflict, refused.Code, refused.Message)
	case errors.Is(err, code.ErrUnavailable):
		return refuse(ctx, unavailable, "git.unavailable", code.ErrUnavailable.Error())
	}
	return fail(ctx, err, "branch", "merging the branch")
}

// deleteFail is fail for deletions: CONFLICT for a branch that moved and a deletion Cobranch won't make, and
// UNAVAILABLE without a Git server.
func deleteFail(ctx context.Context, err error) error {
	var refused *code.DeleteError
	switch {
	case errors.Is(err, code.ErrBranchMoved):
		return refuse(ctx, conflict, "branch.moved", "The branch moved since you read it: read it again.")
	case errors.As(err, &refused):
		return refuse(ctx, conflict, refused.Code, refused.Message)
	case errors.Is(err, code.ErrUnavailable):
		return refuse(ctx, unavailable, "git.unavailable", code.ErrUnavailable.Error())
	}
	return fail(ctx, err, "branch", "deleting the branch")
}

package repository

import (
	"fmt"
	"strconv"
	"time"
)

// ChangeStatus describes how a file was affected by a commit.
type ChangeStatus string

const (
	ChangeStatusAdded    ChangeStatus = "added"
	ChangeStatusModified ChangeStatus = "modified"
	ChangeStatusDeleted  ChangeStatus = "deleted"
	ChangeStatusRenamed  ChangeStatus = "renamed"
)

// DiffLineType is the role of a line within a unified diff hunk.
type DiffLineType string

const (
	DiffLineContext DiffLineType = "context"
	DiffLineAdded   DiffLineType = "added"
	DiffLineDeleted DiffLineType = "deleted"
)

// GitRefType is the kind of git reference: a branch, a tag, or a detached commit.
type GitRefType string

const (
	// GitRefTypeBranch refers to a local branch (refs/heads/*).
	GitRefTypeBranch GitRefType = "BRANCH"
	// GitRefTypeTag refers to an annotated or lightweight tag (refs/tags/*).
	GitRefTypeTag GitRefType = "TAG"
	// GitRefTypeCommit represents a detached HEAD pointing directly at a commit.
	GitRefTypeCommit GitRefType = "COMMIT"
)

func (e GitRefType) IsValid() bool {
	switch e {
	case GitRefTypeBranch, GitRefTypeTag, GitRefTypeCommit:
		return true
	}
	return false
}

func (e GitRefType) String() string { return string(e) }

func (e *GitRefType) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	value := GitRefType(s)
	if !value.IsValid() {
		return fmt.Errorf("%s is not a valid GitRefType", s)
	}
	*e = value
	return nil
}

func (e GitRefType) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(e.String())), nil
}

type RefMeta struct {
	// Full reference name, e.g. refs/heads/main or refs/tags/v1.0.
	Name string `json:"name"`
	// Short name, e.g. main or v1.0.
	ShortName string `json:"shortName"`
	// Whether this reference is a branch or a tag.
	Type GitRefType `json:"type"`
	// Commit hash the reference points to.
	Hash string `json:"hash"`
}

// CommitMeta holds the metadata for a single commit, suitable for listing.
type CommitMeta struct {
	Hash        Hash
	Message     string
	AuthorName  string
	AuthorEmail string
	Date        time.Time
	Parents     []Hash
}

// ChangedFile describes a file that was modified in a commit.
type ChangedFile struct {
	Path    string
	OldPath *string // non-nil for renames
	Status  ChangeStatus
}

// CommitDetail extends CommitMeta with the full message and the list of
// changed files (relative to the first parent).
type CommitDetail struct {
	CommitMeta
	FullMessage string
	Files       []ChangedFile
}

// DiffLine represents one line in a unified diff hunk.
type DiffLine struct {
	Type    DiffLineType
	Content string
	OldLine int
	NewLine int
}

// DiffHunk is a contiguous block of changes in a unified diff.
type DiffHunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []DiffLine
}

// FileDiff is the diff for a single file in a commit.
type FileDiff struct {
	Path     string
	OldPath  *string // non-nil for renames
	IsBinary bool
	IsNew    bool
	IsDelete bool
	Hunks    []DiffHunk
}

// BranchInfo describes a local branch returned by RepoBrowse.Branches.
type BranchInfo struct {
	Name string
	Hash Hash // commit hash
}

// TagInfo describes a tag returned by RepoBrowse.Tags.
type TagInfo struct {
	Name string
	// Hash is always the target commit hash.  For annotated tags the tag
	// object is dereferenced; for lightweight tags this is the ref hash.
	Hash Hash
}

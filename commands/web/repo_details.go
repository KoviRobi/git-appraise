package web

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"

	"github.com/google/git-appraise/repository"
	"github.com/google/git-appraise/review"
)

type BranchDetails struct {
	Ref                 string
	Title               string
	Subtitle            string
	Description         string
	OpenReviewCount     int
	OpenReviews         []review.Summary
	ClosedReviewCount   int
	ClosedReviews       []review.Summary
}

type BranchList []*BranchDetails

func (list BranchList) Len() int      { return len(list) }
func (list BranchList) Swap(i, j int) { list[i], list[j] = list[j], list[i] }
func (list BranchList) Less(i, j int) bool { return list[i].Ref < list[j].Ref }

type RepoDetails struct {
	Path               string
	Repo               repository.Repo
	RepoHash           string
	Title              string
	Subtitle           string
	Description        string
	Branches           BranchList
	AbandonedReviews   []review.Summary
}

var repoDescriptionRe = regexp.MustCompile(`(# (.*)\n)?(## (.*)\n)?((?s).*)`)
const descriptionPath = "README.md"

// Parses the repo description format, a markdown with optional `# Title` and
// `## Subtitle` at the start of the file.
func ParseDescription(text string) (title, subtitle, description string) {
	split := repoDescriptionRe.FindStringSubmatch(text)
	if split[2] != "" {
		title = split[2]
	}
	if split[4] != "" {
		subtitle = split[4]
	}
	description = split[5]
	return
}

func (repoDetails *RepoDetails) UpdateRepoDescription() {
	repoPath := repoDetails.Repo.GetPath()
	repoDetails.Title = path.Base(repoPath)

	descriptionFile := fmt.Sprintf("%s/%s", repoPath, descriptionPath)
	description, err := os.ReadFile(descriptionFile)
	if err == nil {
		repoDetails.Title, repoDetails.Subtitle, repoDetails.Description = ParseDescription(string(description))
	}
}

// NewRepoDetails constructs a RepoDetails instance from the given Repo instance.
func NewRepoDetails(repo repository.Repo) (*RepoDetails, error) {
	repoDetails := &RepoDetails{Repo: repo}
	repoDetails.UpdateRepoDescription()
	return repoDetails, nil
}

// GetBranchDetails constructs a concise summary of the branch.
func (repoDetails *RepoDetails) GetBranchDetails(branch string) *BranchDetails {
	details := &BranchDetails{Ref: branch, Title: branch}
	description, err := repoDetails.Repo.Show(branch, descriptionPath)
	if err == nil {
		details.Title, details.Subtitle, details.Description = ParseDescription(description)
	}
	return details
}

func (repoDetails *RepoDetails) Update() error {
	stateHash, err := repoDetails.Repo.GetRepoStateHash()
	if err != nil {
		return err
	}
	if stateHash == repoDetails.RepoHash {
		return nil
	}

	repoDetails.UpdateRepoDescription()

	branchesSet := make(map[string]*BranchDetails)
	allReviews := review.ListAll(repoDetails.Repo)
	openReviews := make(map[string][]review.Summary)
	closedReviews := make(map[string][]review.Summary)
	var abandonedReviews []review.Summary
	for _, review := range allReviews {
		if review.Request.TargetRef == "" {
			abandonedReviews = append(abandonedReviews, review)
		} else {
			branch := review.Request.TargetRef
			if branchesSet[branch] == nil {
				branchesSet[branch] = repoDetails.GetBranchDetails(branch)
			}
			if review.Submitted {
				closedReviews[branch] = append(closedReviews[branch], review)
			} else {
				openReviews[branch] = append(openReviews[branch], review)
			}
		}
	}

	var branches BranchList
	for _, branch := range branchesSet {
		slices.Reverse(openReviews[branch.Ref])
		slices.Reverse(closedReviews[branch.Ref])

		branch.OpenReviewCount   = len(openReviews[branch.Ref])
		branch.OpenReviews       = openReviews[branch.Ref]
		branch.ClosedReviewCount = len(closedReviews[branch.Ref])
		branch.ClosedReviews     = closedReviews[branch.Ref]

		branches = append(branches, branch)
	}
	sort.Stable(branches)

	repoDetails.Branches         = branches
	repoDetails.AbandonedReviews = abandonedReviews
	repoDetails.RepoHash         = stateHash
	return nil
}

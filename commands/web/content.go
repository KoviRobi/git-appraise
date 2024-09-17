package web

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/KoviRobi/git-appraise/commands/output"
	"github.com/KoviRobi/git-appraise/repository"
	"github.com/KoviRobi/git-appraise/review"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"

	"github.com/microcosm-cc/bluemonday"
)

const (
	// SHA1 produces 160 bit hashes, so a hex-encoded hash should be no more than 40 characters.
	maxHashLength = 40
)

var (
	//go:embed stylesheet.css
	stylesheet_css string

	//go:embed repo.html
	repo_html string

	//go:embed branch.html
	branch_html string

	//go:embed review.html
	review_html string
)

func checkStringLooksLikeHash(s string) error {
	if len(s) > maxHashLength {
		return errors.New("Invalid hash parameter")
	}
	for _, c := range s {
		if ((c < 'a') || (c > 'f')) && ((c < '0') || (c > '9')) {
			return errors.New("Invalid hash character")
		}
	}
	return nil
}

type Paths interface {
	Css() string
	Repo() string
	Branch(branch uint64) string
	Review(branch uint64, review string) string
}

type ServePaths struct {}

func (ServePaths) Css() string { return "stylesheet.css" }
func (ServePaths) Repo() string { return "repo.html" }
func (ServePaths) Branch(branch uint64) string {
	return fmt.Sprintf("branch.html?branch=%d", branch)
}
func (ServePaths) Review(branch uint64, review string) string {
	return fmt.Sprintf("review.html?branch=%d&review=%s", branch, review)
}

type StaticPaths struct {}

func (StaticPaths) Css() string { return "stylesheet.css" }
func (StaticPaths) Repo() string { return "index.html" }
func (StaticPaths) Branch(branch uint64) string {
	return fmt.Sprintf("branch_%d.html", branch)
}
func (StaticPaths) Review(branch uint64, review string) string {
	return fmt.Sprintf("review_%d_%s.html", branch, review)
}

func mdToHTML(md []byte) []byte {
	// create markdown parser with extensions
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	// create HTML renderer with extensions
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	maybeUnsafeHTML := markdown.Render(doc, renderer)
	html := bluemonday.UGCPolicy().SanitizeBytes(maybeUnsafeHTML)

	return html
}

func ServeTemplate(v interface{}, p Paths, w io.Writer, name string, templ string) error {
	tmpl := template.New(name)
	tmpl = tmpl.Funcs(map[string]any{
		"u64": func(i int) uint64 { return uint64(i) },
		"addu64": func(a, b uint64) uint64 { return a + b },
		"startOfHunk": func(a uint64) uint64 { return max(1, a) - 1; },
		"opName": func(op repository.DiffOp) string {
			switch op {
			case repository.OpContext: return "context"
			case repository.OpDelete: return "delete"
			case repository.OpAdd: return "add"
			default: return "unknown"
			}
		},
		"isLHS": func(op repository.DiffOp) bool {
			return op == repository.OpContext || op == repository.OpDelete
		},
		"isRHS": func(op repository.DiffOp) bool {
			return op == repository.OpContext || op == repository.OpAdd
		},
		"mdToHTML": func(s string) template.HTML { return template.HTML(mdToHTML([]byte(s))) },
		"paths": func() Paths { return p },
	})
	tmpl, err := tmpl.Parse(templ)
	if err != nil {
		return err
	}
	var writer bytes.Buffer
	err = tmpl.Execute(&writer, v)
	if err != nil {
		return err
	}
	_, err = w.Write(writer.Bytes())
	return err
}

func ServeErrorTemplate(err error, code int, w http.ResponseWriter) {
		http.Error(w, err.Error(), code)
}

func (repoDetails *RepoDetails) ServeStyleSheet(w http.ResponseWriter, r *http.Request) {
	var writer bytes.Buffer
	err := repoDetails.WriteStyleSheet(&writer)
	if err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(writer.Bytes())
}

func (repoDetails *RepoDetails) WriteStyleSheet(w io.Writer) error {
	_, err := w.Write([]byte(stylesheet_css))

	return err
}

// Lists branches
func (repoDetails *RepoDetails) ServeRepoTemplate(w http.ResponseWriter, r *http.Request) {
	if err := repoDetails.Update(); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	var writer bytes.Buffer
	if err := repoDetails.WriteRepoTemplate(ServePaths{}, &writer); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(writer.Bytes())
}

func (repoDetails *RepoDetails) WriteRepoTemplate(p Paths, w io.Writer) error {
	return ServeTemplate(repoDetails, p, w, "repo", repo_html)
}

// Shows reviews for a given branch
// The branch to summarize is given by the 'repo' URL parameter.
func (repoDetails *RepoDetails) ServeBranchTemplate(w http.ResponseWriter, r *http.Request) {
	if err := repoDetails.Update(); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	branchParam := r.URL.Query().Get("branch")
	if branchParam == "" {
		ServeErrorTemplate(errors.New("No branch specified"), http.StatusBadRequest, w)
		return
	}
	branchNum, err := strconv.ParseUint(branchParam, 10, 32)
	if err != nil || len(repoDetails.Branches) <= int(branchNum) {
		ServeErrorTemplate(errors.New("Bad branch specified"), http.StatusBadRequest, w)
		return
	}
	var writer bytes.Buffer
	if err := repoDetails.WriteBranchTemplate(branchNum, ServePaths{}, &writer); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(writer.Bytes())
}

func (repoDetails *RepoDetails) WriteBranchTemplate(branch uint64, p Paths, w io.Writer) error {
	type templateArgs struct {
		RepoDetails   *RepoDetails
		BranchNum     uint64
		BranchDetails *BranchDetails
	}
	args := templateArgs {
		RepoDetails: repoDetails,
		BranchNum: branch,
		BranchDetails: repoDetails.Branches[branch],
	}
	return ServeTemplate(args, p, w, "branch", branch_html)
}

// Show a review with inline diff
// The enclosing repository is given by the 'repo' URL parameter.
// The review to write is given by the 'review' URL parameter.
func (repoDetails *RepoDetails) ServeReviewTemplate(w http.ResponseWriter, r *http.Request) {
	if err := repoDetails.Update(); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	branchParam := r.URL.Query().Get("branch")
	if branchParam == "" {
		ServeErrorTemplate(errors.New("No branch specified"), http.StatusBadRequest, w)
		return
	}
	branchNum, err := strconv.ParseUint(branchParam, 10, 32)
	if err != nil || len(repoDetails.Branches) <= int(branchNum) {
		ServeErrorTemplate(errors.New("Bad branch specified"), http.StatusBadRequest, w)
		return
	}
	reviewParam := r.URL.Query().Get("review")
	if reviewParam == "" {
		ServeErrorTemplate(errors.New("No review specified"), http.StatusBadRequest, w)
		return
	}
	if err := checkStringLooksLikeHash(reviewParam); err != nil {
		ServeErrorTemplate(err, http.StatusBadRequest, w)
		return
	}
	var writer bytes.Buffer
	if err := repoDetails.WriteReviewTemplate(branchNum, reviewParam, ServePaths{}, &writer); err != nil {
		ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(writer.Bytes())
}

func (repoDetails *RepoDetails) WriteReviewTemplate(branch uint64, reviewID string, p Paths, w io.Writer) error {
	reviewDetails, err := review.Get(repoDetails.Repo, reviewID)
	if err != nil {
		return err
	}
	commit := reviewDetails.Summary.Revision
	commitDetails, err := repoDetails.Repo.GetCommitDetails(commit)
	if err != nil {
		return err
	}
	commitMessage, err := reviewDetails.Repo.GetCommitMessage(commit)
	if err != nil {
		return err
	}
	// Show only the review commit
	diffs, err := reviewDetails.Repo.ParsedDiff1(commit)
	if err != nil {
		return err
	}

	var commitThreads = make(map[uint32][]review.CommentThread)
	var lineThreads = make(map[string]map[uint32][]review.CommentThread)
	output.SeparateComments(reviewDetails.Summary.Comments, commitThreads, lineThreads)

	type templateArgs struct {
		RepoDetails *RepoDetails
		BranchNum uint64
		BranchDetails *BranchDetails
		CommitHash string
		CommitDetails *repository.CommitDetails
		CommitLines []string
		CommitThreads map[uint32][]review.CommentThread
		ReviewDetails *review.Review
		LineThreads map[string]map[uint32][]review.CommentThread
		Diffs []repository.FileDiff
	}
	args := templateArgs{
		RepoDetails: repoDetails,
		BranchNum: branch,
		BranchDetails: repoDetails.Branches[branch],
		CommitHash: commit,
		CommitDetails: commitDetails,
		CommitLines: strings.Split(commitMessage, "\n"),
		CommitThreads: commitThreads,
		ReviewDetails: reviewDetails,
		LineThreads: lineThreads,
		Diffs: diffs,
	}

	return ServeTemplate(args, p, w, "review", review_html)
}

// ServeEntryPointRedirect writes the main redirect response to the given writer.
func (repoDetails *RepoDetails) ServeEntryPointRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/repo.html", http.StatusTemporaryRedirect)
	return
}

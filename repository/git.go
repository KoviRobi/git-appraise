/*
Copyright 2015 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package repository contains helper methods for working with the Git repo.
package repository

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	exec "golang.org/x/sys/execabs"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

const (
	branchRefPrefix         = "refs/heads/"
	notesRefPrefix          = "refs/notes/"
	devtoolsRefPrefix       = "refs/devtools/"
	remoteDevtoolsRefPrefix = "refs/remoteDevtools/"
)

// GitRepo represents an instance of a (local) git repository.
type GitRepo struct {
	Path string
}

// Run the given git command with the given I/O reader/writers and environment, returning an error if it fails.
func (repo *GitRepo) runGitCommandWithIOAndEnv(stdin io.Reader, stdout, stderr io.Writer, env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo.Path
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env
	return cmd.Run()
}

// Run the given git command with the given I/O reader/writers, returning an error if it fails.
func (repo *GitRepo) runGitCommandWithIO(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	return repo.runGitCommandWithIOAndEnv(stdin, stdout, stderr, nil, args...)
}

// Run the given git command and return its stdout, or an error if the command fails.
func (repo *GitRepo) runGitCommandRaw(args ...string) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := repo.runGitCommandWithIO(nil, &stdout, &stderr, args...)
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// Run the given git command and return its stdout, or an error if the command fails.
func (repo *GitRepo) runGitCommand(args ...string) (string, error) {
	stdout, stderr, err := repo.runGitCommandRaw(args...)
	if err != nil {
		if stderr == "" {
			stderr = "Error running git command: " + strings.Join(args, " ")
		}
		err = fmt.Errorf(stderr)
	}
	return stdout, err
}

// Run the given git command and return its stdout, or an error if the command fails.
func (repo *GitRepo) runGitCommandWithEnv(env []string, args ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := repo.runGitCommandWithIOAndEnv(nil, &stdout, &stderr, env, args...)
	if err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr == "" {
			stderrStr = "Error running git command: " + strings.Join(args, " ")
		}
		err = fmt.Errorf(stderrStr)
	}
	return strings.TrimSpace(stdout.String()), err
}

// Run the given git command using the same stdin, stdout, and stderr as the review tool.
func (repo *GitRepo) runGitCommandInline(args ...string) error {
	return repo.runGitCommandWithIO(os.Stdin, os.Stdout, os.Stderr, args...)
}

// NewGitRepo determines if the given working directory is inside of a git repository,
// and returns the corresponding GitRepo instance if it is.
func NewGitRepo(path string) (*GitRepo, error) {
	repo := &GitRepo{Path: path}
	_, _, err := repo.runGitCommandRaw("rev-parse")
	if err == nil {
		return repo, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil, err
	}
	return nil, err
}

func (repo *GitRepo) HasRef(ref string) (bool, error) {
	_, _, err := repo.runGitCommandRaw("show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	// Got an unexpected error
	return false, err
}

// HasObject returns whether or not the repo contains an object with the given hash.
func (repo *GitRepo) HasObject(hash string) (bool, error) {
	_, err := repo.runGitCommand("cat-file", "-e", hash)
	if err == nil {
		// We verified the object exists
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	// Got an unexpected error
	return false, err
}

// GetPath returns the path to the repo.
func (repo *GitRepo) GetPath() string {
	return repo.Path
}

// GetDataDir returns the path to the repo data area, e.g. `.git` directory for
// git.
func (repo *GitRepo) GetDataDir() (string, error) {
	return repo.runGitCommand("rev-parse", "--git-dir")
}

// GetRepoStateHash returns a hash which embodies the entire current state of a repository.
func (repo *GitRepo) GetRepoStateHash() (string, error) {
	stateSummary, err := repo.runGitCommand("show-ref")
	return fmt.Sprintf("%x", sha1.Sum([]byte(stateSummary))), err
}

// GetUserEmail returns the email address that the user has used to configure git.
func (repo *GitRepo) GetUserEmail() (string, error) {
	return repo.runGitCommand("config", "user.email")
}

// GetUserSigningKey returns the key id the user has configured for
// sigining git artifacts.
func (repo *GitRepo) GetUserSigningKey() (string, error) {
	return repo.runGitCommand("config", "user.signingKey")
}

// GetCoreEditor returns the name of the editor that the user has used to configure git.
func (repo *GitRepo) GetCoreEditor() (string, error) {
	return repo.runGitCommand("var", "GIT_EDITOR")
}

// GetSubmitStrategy returns the way in which a review is submitted
func (repo *GitRepo) GetSubmitStrategy() (string, error) {
	submitStrategy, _ := repo.runGitCommand("config", "appraise.submit")
	return submitStrategy, nil
}

// HasUncommittedChanges returns true if there are local, uncommitted changes.
func (repo *GitRepo) HasUncommittedChanges() (bool, error) {
	out, err := repo.runGitCommand("status", "--porcelain")
	if err != nil {
		return false, err
	}
	if len(out) > 0 {
		return true, nil
	}
	return false, nil
}

// VerifyCommit verifies that the supplied hash points to a known commit.
func (repo *GitRepo) VerifyCommit(hash string) error {
	out, err := repo.runGitCommand("cat-file", "-t", hash)
	if err != nil {
		return err
	}
	objectType := strings.TrimSpace(string(out))
	if objectType != "commit" {
		return fmt.Errorf("Hash %q points to a non-commit object of type %q", hash, objectType)
	}
	return nil
}

// VerifyGitRef verifies that the supplied ref points to a known commit.
func (repo *GitRepo) VerifyGitRef(ref string) error {
	_, err := repo.runGitCommand("show-ref", "--verify", ref)
	return err
}

// GetHeadRef returns the ref that is the current HEAD.
func (repo *GitRepo) GetHeadRef() (string, error) {
	return repo.runGitCommand("symbolic-ref", "HEAD")
}

// GetCommitHash returns the hash of the commit pointed to by the given ref.
func (repo *GitRepo) GetCommitHash(ref string) (string, error) {
	return repo.runGitCommand("show", "-s", "--format=%H", ref, "--")
}

// ResolveRefCommit returns the commit pointed to by the given ref, which may be a remote ref.
//
// This differs from GetCommitHash which only works on exact matches, in that it will try to
// intelligently handle the scenario of a ref not existing locally, but being known to exist
// in a remote repo.
//
// This method should be used when a command may be performed by either the reviewer or the
// reviewee, while GetCommitHash should be used when the encompassing command should only be
// performed by the reviewee.
func (repo *GitRepo) ResolveRefCommit(ref string) (string, error) {
	if err := repo.VerifyGitRef(ref); err == nil {
		return repo.GetCommitHash(ref)
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		// The ref is a branch. Check if it exists in exactly one remote
		pattern := strings.Replace(ref, "refs/heads", "**", 1)
		matchingOutput, err := repo.runGitCommand("for-each-ref", "--format=%(refname)", pattern)
		if err != nil {
			return "", err
		}
		matchingRefs := strings.Split(matchingOutput, "\n")
		if len(matchingRefs) == 1 && matchingRefs[0] != "" {
			// There is exactly one match
			return repo.GetCommitHash(matchingRefs[0])
		}
		return "", fmt.Errorf("Unable to find a git ref matching the pattern %q", pattern)
	}
	return "", fmt.Errorf("Unknown git ref %q", ref)
}

// GetCommitMessage returns the message stored in the commit pointed to by the given ref.
func (repo *GitRepo) GetCommitMessage(ref string) (string, error) {
	return repo.runGitCommand("show", "-s", "--format=%B", ref, "--")
}

// GetCommitTime returns the commit time of the commit pointed to by the given ref.
func (repo *GitRepo) GetCommitTime(ref string) (string, error) {
	return repo.runGitCommand("show", "-s", "--format=%ct", ref, "--")
}

// GetLastParent returns the last parent of the given commit (as ordered by git).
func (repo *GitRepo) GetLastParent(ref string) (string, error) {
	return repo.runGitCommand("rev-list", "--skip", "1", "-n", "1", ref)
}

// GetCommitDetails returns the details of a commit's metadata.
func (repo GitRepo) GetCommitDetails(ref string) (*CommitDetails, error) {
	var err error
	show := func(formatString string) (result string) {
		if err != nil {
			return ""
		}
		result, err = repo.runGitCommand("show", "-s", fmt.Sprintf("--format=tformat:%s", formatString), ref, "--")
		return result
	}

	jsonFormatString := "{\"tree\":\"%T\", \"time\": \"%at\"}"
	detailsJSON := show(jsonFormatString)
	if err != nil {
		return nil, err
	}
	var details CommitDetails
	err = json.Unmarshal([]byte(detailsJSON), &details)
	if err != nil {
		return nil, err
	}
	details.Author = show("%an")
	details.AuthorEmail = show("%ae")
	details.Committer = show("%cn")
	details.CommitterEmail = show("%ce")
	details.Summary = show("%s")
	parentsString := show("%P")
	details.Parents = strings.Split(parentsString, " ")
	if err != nil {
		return nil, err
	}
	return &details, nil
}

// MergeBase determines if the first commit that is an ancestor of the two arguments.
func (repo *GitRepo) MergeBase(a, b string) (string, error) {
	return repo.runGitCommand("merge-base", a, b)
}

// IsAncestor determines if the first argument points to a commit that is an ancestor of the second.
func (repo *GitRepo) IsAncestor(ancestor, descendant string) (bool, error) {
	_, _, err := repo.runGitCommandRaw("merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, fmt.Errorf("Error while trying to determine commit ancestry: %v", err)
}

// Diff computes the diff between two given commits.
func (repo *GitRepo) Diff(left, right string, diffArgs ...string) (string, error) {
	args := []string{"diff"}
	args = append(args, diffArgs...)
	args = append(args, fmt.Sprintf("%s..%s", left, right))
	return repo.runGitCommand(args...)
}

func (repo *GitRepo) Diff1(commit string, diffArgs ...string) (string, error) {
	args := []string{"show", "--format=", "--patch"}
	args = append(args, diffArgs...)
	args = append(args, commit)
	args = append(args, "--")
	return repo.runGitCommand(args...)
}

// Diff computes the diff between two given commits.
func (repo *GitRepo) ParsedDiff(left, right string, diffArgs ...string) ([]FileDiff, error) {
	if !slices.Contains(diffArgs, "--no-ext-diff") {
		diffArgs = append(diffArgs, "--no-ext-diff")
	}
	diff, err := repo.Diff(left, right, diffArgs...)
	if err != nil {
		return nil, err
	}

	return parsedDiff(diff)
}

func (repo *GitRepo) ParsedDiff1(commit string, diffArgs ...string) ([]FileDiff, error) {
	if !slices.Contains(diffArgs, "--no-ext-diff") {
		diffArgs = append(diffArgs, "--no-ext-diff")
	}
	diff, err := repo.Diff1(commit, diffArgs...)
	if err != nil {
		return nil, err
	}

	return parsedDiff(diff)
}

func parsedDiff(diff string) ([]FileDiff, error) {
	files, _, err := gitdiff.Parse(strings.NewReader(diff))
	if err != nil {
		return nil, err
	}

	var fileDiff []FileDiff

	for _, file := range files {

		var fragments []DiffFragment
		for _, fragment := range file.TextFragments {

			var lines []DiffLine
			for _, line := range fragment.Lines {
				var op DiffOp
				switch line.Op {
				case gitdiff.OpContext: op = OpContext
				case gitdiff.OpAdd: op = OpAdd
				case gitdiff.OpDelete: op = OpDelete
				}
				lines = append(lines, DiffLine{
					Op: op,
					Line: strings.Trim(line.Line, "\n"),
				})
			}

			fragments = append(fragments, DiffFragment{
				Comment: fragment.Comment,
				OldPosition: uint64(fragment.OldPosition),
				OldLines: uint64(fragment.OldLines),
				NewPosition: uint64(fragment.NewPosition),
				NewLines: uint64(fragment.NewLines),
				LinesAdded: uint64(fragment.LinesAdded),
				LinesDeleted: uint64(fragment.LinesDeleted),
				LeadingContext: uint64(fragment.LeadingContext),
				TrailingContext: uint64(fragment.TrailingContext),
				Lines: lines,
			})
		}

		fileDiff = append(fileDiff, FileDiff{
			OldName: file.OldName,
			NewName: file.NewName,
			Fragments: fragments,
		})
	}

	return fileDiff, nil
}

// Show returns the contents of the given file at the given commit.
func (repo *GitRepo) Show(commit, path string) (string, error) {
	return repo.runGitCommand("show", fmt.Sprintf("%s:%s", commit, path), "--")
}

// SwitchToRef changes the currently-checked-out ref.
func (repo *GitRepo) SwitchToRef(ref string) error {
	// If the ref starts with "refs/heads/", then we have to trim that prefix,
	// or else we will wind up in a detached HEAD state.
	if strings.HasPrefix(ref, branchRefPrefix) {
		ref = ref[len(branchRefPrefix):]
	}
	_, err := repo.runGitCommand("checkout", ref)
	return err
}

// mergeArchives merges two archive refs.
func (repo *GitRepo) mergeArchives(archive, remoteArchive string) error {
	hasRemote, err := repo.HasRef(remoteArchive)
	if err != nil {
		return err
	}
	if !hasRemote {
		// The remote archive does not exist, so we have nothing to do
		return nil
	}
	remoteHash, err := repo.GetCommitHash(remoteArchive)
	if err != nil {
		return err
	}

	hasLocal, err := repo.HasRef(archive)
	if err != nil {
		return err
	}
	if !hasLocal {
		// The local archive does not exist, so we merely need to set it
		_, err := repo.runGitCommand("update-ref", archive, remoteHash)
		return err
	}
	archiveHash, err := repo.GetCommitHash(archive)
	if err != nil {
		return err
	}

	isAncestor, err := repo.IsAncestor(archiveHash, remoteHash)
	if err != nil {
		return err
	}
	if isAncestor {
		// The archive can simply be fast-forwarded
		_, err := repo.runGitCommand("update-ref", archive, remoteHash, archiveHash)
		return err
	}

	// Create a merge commit of the two archives
	refDetails, err := repo.GetCommitDetails(remoteArchive)
	if err != nil {
		return err
	}
	newArchiveHash, err := repo.runGitCommand("commit-tree", "-p", remoteHash, "-p", archiveHash, "-m", "Merge local and remote archives", refDetails.Tree)
	if err != nil {
		return err
	}
	newArchiveHash = strings.TrimSpace(newArchiveHash)
	_, err = repo.runGitCommand("update-ref", archive, newArchiveHash, archiveHash)
	return err
}

// ArchiveRef adds the current commit pointed to by the 'ref' argument
// under the ref specified in the 'archive' argument.
//
// Both the 'ref' and 'archive' arguments are expected to be the fully
// qualified names of git refs (e.g. 'refs/heads/my-change' or
// 'refs/devtools/archives/reviews').
//
// If the ref pointed to by the 'archive' argument does not exist
// yet, then it will be created.
func (repo *GitRepo) ArchiveRef(ref, archive string) error {
	refHash, err := repo.GetCommitHash(ref)
	if err != nil {
		return err
	}
	refDetails, err := repo.GetCommitDetails(ref)
	if err != nil {
		return err
	}

	commitTreeArgs := []string{"commit-tree"}
	archiveHash, err := repo.GetCommitHash(archive)
	if err != nil {
		archiveHash = ""
	} else {
		if isAncestor, err := repo.IsAncestor(refHash, archiveHash); err != nil {
			return err
		} else if isAncestor {
			// The ref has already been archived, so we have nothing to do
			return nil
		}
		commitTreeArgs = append(commitTreeArgs, "-p", archiveHash)
	}
	commitTreeArgs = append(commitTreeArgs, "-p", refHash, "-m", fmt.Sprintf("Archive %s", refHash), refDetails.Tree)
	newArchiveHash, err := repo.runGitCommand(commitTreeArgs...)
	if err != nil {
		return err
	}
	newArchiveHash = strings.TrimSpace(newArchiveHash)
	updateRefArgs := []string{"update-ref", archive, newArchiveHash}
	if archiveHash != "" {
		updateRefArgs = append(updateRefArgs, archiveHash)
	}
	_, err = repo.runGitCommand(updateRefArgs...)
	return err
}

// MergeRef merges the given ref into the current one.
//
// The ref argument is the ref to merge, and fastForward indicates that the
// current ref should only move forward, as opposed to creating a bubble merge.
// The messages argument(s) provide text that should be included in the default
// merge commit message (separated by blank lines).
func (repo *GitRepo) MergeRef(ref string, fastForward bool, messages ...string) error {
	args := []string{"merge"}
	if fastForward {
		args = append(args, "--ff", "--ff-only")
	} else {
		args = append(args, "--no-ff")
	}
	if len(messages) > 0 {
		commitMessage := strings.Join(messages, "\n\n")
		args = append(args, "-e", "-m", commitMessage)
	}
	args = append(args, ref)
	return repo.runGitCommandInline(args...)
}

// MergeAndSignRef merges the given ref into the current one and signs the
// merge.
//
// The ref argument is the ref to merge, and fastForward indicates that the
// current ref should only move forward, as opposed to creating a bubble merge.
// The messages argument(s) provide text that should be included in the default
// merge commit message (separated by blank lines).
func (repo *GitRepo) MergeAndSignRef(ref string, fastForward bool,
	messages ...string) error {

	args := []string{"merge"}
	if fastForward {
		args = append(args, "--ff", "--ff-only", "-S")
	} else {
		args = append(args, "--no-ff", "-S")
	}
	if len(messages) > 0 {
		commitMessage := strings.Join(messages, "\n\n")
		args = append(args, "-e", "-m", commitMessage)
	}
	args = append(args, ref)
	return repo.runGitCommandInline(args...)
}

// RebaseRef rebases the current ref onto the given one.
func (repo *GitRepo) RebaseRef(ref string) error {
	return repo.runGitCommandInline("rebase", "-i", ref)
}

// RebaseAndSignRef rebases the current ref onto the given one and signs the
// result.
func (repo *GitRepo) RebaseAndSignRef(ref string) error {
	return repo.runGitCommandInline("rebase", "-S", "-i", ref)
}

// ListCommits returns the list of commits reachable from the given ref.
//
// The generated list is in chronological order (with the oldest commit first).
//
// If the specified ref does not exist, then this method returns an empty result.
func (repo *GitRepo) ListCommits(ref string) []string {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := repo.runGitCommandWithIO(nil, &stdout, &stderr, "rev-list", "--reverse", ref); err != nil {
		return nil
	}

	byteLines := bytes.Split(stdout.Bytes(), []byte("\n"))
	var commits []string
	for _, byteLine := range byteLines {
		commits = append(commits, string(byteLine))
	}
	return commits
}

// ListCommitsBetween returns the list of commits between the two given revisions.
//
// The "from" parameter is the starting point (exclusive), and the "to"
// parameter is the ending point (inclusive).
//
// The "from" commit does not need to be an ancestor of the "to" commit. If it
// is not, then the merge base of the two is used as the starting point.
// Admittedly, this makes calling these the "between" commits is a bit of a
// misnomer, but it also makes the method easier to use when you want to
// generate the list of changes in a feature branch, as it eliminates the need
// to explicitly calculate the merge base. This also makes the semantics of the
// method compatible with git's built-in "rev-list" command.
//
// The generated list is in chronological order (with the oldest commit first).
func (repo *GitRepo) ListCommitsBetween(from, to string) ([]string, error) {
	out, err := repo.runGitCommand("rev-list", "--reverse", from+".."+to)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// StoreBlob writes the given file to the repository and returns its hash.
func (repo *GitRepo) StoreBlob(contents string) (string, error) {
	stdin := strings.NewReader(contents)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{"hash-object", "-w", "-t", "blob", "--stdin"}
	err := repo.runGitCommandWithIO(stdin, &stdout, &stderr, args...)
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		return "", fmt.Errorf("failure storing a git blob, %v: %q", err, message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// StoreTree writes the given file tree to the repository and returns its hash.
func (repo *GitRepo) StoreTree(contents map[string]TreeChild) (string, error) {
	var lines []string
	for path, obj := range contents {
		objHash, err := obj.Store(repo)
		if err != nil {
			return "", err
		}
		mode := "040000"
		if obj.Type() == "blob" {
			mode = "100644"
		}
		line := fmt.Sprintf("%s %s %s\t%s", mode, obj.Type(), objHash, path)
		lines = append(lines, line)
	}
	stdin := strings.NewReader(strings.Join(lines, "\n"))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{"mktree"}
	err := repo.runGitCommandWithIO(stdin, &stdout, &stderr, args...)
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		return "", fmt.Errorf("failure storing a git tree, %v: %q", err, message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (repo *GitRepo) readBlob(objHash string) (*Blob, error) {
	out, err := repo.runGitCommand("cat-file", "-p", objHash)
	if err != nil {
		return nil, fmt.Errorf("failure reading the file contents of %q: %v", objHash, err)
	}
	return &Blob{contents: out, savedHashes: map[Repo]string{repo: objHash}}, nil
}

func (repo *GitRepo) ReadTree(ref string) (*Tree, error) {
	return repo.readTreeWithHash(ref, "")
}

func (repo *GitRepo) readTreeWithHash(ref, hash string) (*Tree, error) {
	out, err := repo.runGitCommand("ls-tree", "--full-tree", ref)
	if err != nil {
		return nil, fmt.Errorf("failure listing the file contents of %q: %v", ref, err)
	}
	contents := make(map[string]TreeChild)
	if len(out) == 0 {
		// This is possible if the tree is empty
		return NewTree(contents), nil
	}
	for _, line := range strings.Split(out, "\n") {
		lineParts := strings.Split(line, "\t")
		if len(lineParts) != 2 {
			return nil, fmt.Errorf("malformed ls-tree output line: %q", line)
		}
		path := lineParts[1]
		lineParts = strings.Split(lineParts[0], " ")
		if len(lineParts) != 3 {
			return nil, fmt.Errorf("malformed ls-tree output line: %q", line)
		}
		objType := lineParts[1]
		objHash := lineParts[2]
		var child TreeChild
		if objType == "tree" {
			child, err = repo.readTreeWithHash(objHash, objHash)
		} else if objType == "blob" {
			child, err = repo.readBlob(objHash)
		} else {
			return nil, fmt.Errorf("unrecognized tree object type: %q", objType)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read a tree child object: %v", err)
		}
		contents[path] = child
	}
	t := NewTree(contents)
	t.savedHashes[repo] = hash
	return t, nil
}

// CreateCommit creates a commit object and returns its hash.
func (repo *GitRepo) CreateCommit(details *CommitDetails) (string, error) {
	args := []string{"commit-tree", details.Tree, "-m", details.Summary}
	for _, parent := range details.Parents {
		args = append(args, "-p", parent)
	}
	var env []string
	if details.Author != "" {
		env = append(env, fmt.Sprintf("GIT_AUTHOR_NAME=%s", details.Author))
	}
	if details.AuthorEmail != "" {
		env = append(env, fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", details.AuthorEmail))
	}
	if details.AuthorTime != "" {
		env = append(env, fmt.Sprintf("GIT_AUTHOR_DATE=%s", details.AuthorTime))
	}
	if details.Committer != "" {
		env = append(env, fmt.Sprintf("GIT_COMMITTER_NAME=%s", details.Committer))
	}
	if details.CommitterEmail != "" {
		env = append(env, fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", details.CommitterEmail))
	}
	if details.Time != "" {
		env = append(env, fmt.Sprintf("GIT_COMMITTER_DATE=%s", details.Time))
	}
	return repo.runGitCommandWithEnv(env, args...)
}

// CreateCommitWithTree creates a commit object with the given tree and returns its hash.
func (repo *GitRepo) CreateCommitWithTree(details *CommitDetails, t *Tree) (string, error) {
	treeHash, err := repo.StoreTree(t.Contents())
	if err != nil {
		return "", fmt.Errorf("failure storing a tree: %v", err)
	}
	details.Tree = treeHash
	return repo.CreateCommit(details)
}

// SetRef sets the commit pointed to by the specified ref to `newCommitHash`,
// iff the ref currently points `previousCommitHash`.
func (repo *GitRepo) SetRef(ref, newCommitHash, previousCommitHash string) error {
	args := []string{"update-ref", ref, newCommitHash}
	if previousCommitHash != "" {
		args = append(args, previousCommitHash)
	}
	_, err := repo.runGitCommand(args...)
	return err
}

// GetNotes uses the "git" command-line tool to read the notes from the given ref for a given revision.
func (repo *GitRepo) GetNotes(notesRef, revision string) []Note {
	var notes []Note
	rawNotes, err := repo.runGitCommand("notes", "--ref", notesRef, "show", revision, "--")
	if err != nil {
		// We just assume that this means there are no notes
		return nil
	}
	for _, line := range strings.Split(rawNotes, "\n") {
		notes = append(notes, Note([]byte(line)))
	}
	return notes
}

func stringsReader(s []*string) io.Reader {
	var subReaders []io.Reader
	for _, strPtr := range s {
		subReader := strings.NewReader(*strPtr)
		subReaders = append(subReaders, subReader, strings.NewReader("\n"))
	}
	return io.MultiReader(subReaders...)
}

// splitBatchCheckOutput parses the output of a 'git cat-file --batch-check=...' command.
//
// The output is expected to be formatted as a series of entries, with each
// entry consisting of:
// 1. The SHA1 hash of the git object being output, followed by a space.
// 2. The git "type" of the object (commit, blob, tree, missing, etc), followed by a newline.
//
// To generate this format, make sure that the 'git cat-file' command includes
// the argument '--batch-check=%(objectname) %(objecttype)'.
//
// The return value is a map from object hash to a boolean indicating if that object is a commit.
func splitBatchCheckOutput(out *bytes.Buffer) (map[string]bool, error) {
	isCommit := make(map[string]bool)
	reader := bufio.NewReader(out)
	for {
		nameLine, err := reader.ReadString(byte(' '))
		if err == io.EOF {
			return isCommit, nil
		}
		if err != nil {
			return nil, fmt.Errorf("Failure while reading the next object name: %v", err)
		}
		nameLine = strings.TrimSuffix(nameLine, " ")
		typeLine, err := reader.ReadString(byte('\n'))
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("Failure while reading the next object type: %q - %v", nameLine, err)
		}
		typeLine = strings.TrimSuffix(typeLine, "\n")
		if typeLine == "commit" {
			isCommit[nameLine] = true
		}
	}
}

// splitBatchCatFileOutput parses the output of a 'git cat-file --batch=...' command.
//
// The output is expected to be formatted as a series of entries, with each
// entry consisting of:
// 1. The SHA1 hash of the git object being output, followed by a newline.
// 2. The size of the object's contents in bytes, followed by a newline.
// 3. The objects contents.
//
// To generate this format, make sure that the 'git cat-file' command includes
// the argument '--batch=%(objectname)\n%(objectsize)'.
func splitBatchCatFileOutput(out *bytes.Buffer) (map[string][]byte, error) {
	contentsMap := make(map[string][]byte)
	reader := bufio.NewReader(out)
	for {
		nameLine, err := reader.ReadString(byte('\n'))
		if strings.HasSuffix(nameLine, "\n") {
			nameLine = strings.TrimSuffix(nameLine, "\n")
		}
		if err == io.EOF {
			return contentsMap, nil
		}
		if err != nil {
			return nil, fmt.Errorf("Failure while reading the next object name: %v", err)
		}
		sizeLine, err := reader.ReadString(byte('\n'))
		if strings.HasSuffix(sizeLine, "\n") {
			sizeLine = strings.TrimSuffix(sizeLine, "\n")
		}
		if err != nil {
			return nil, fmt.Errorf("Failure while reading the next object size: %q - %v", nameLine, err)
		}
		size, err := strconv.Atoi(sizeLine)
		if err != nil {
			return nil, fmt.Errorf("Failure while parsing the next object size: %q - %v", nameLine, err)
		}
		contentBytes := make([]byte, size, size)
		readDest := contentBytes
		len := 0
		err = nil
		for err == nil && len < size {
			nextLen := 0
			nextLen, err = reader.Read(readDest)
			len += nextLen
			readDest = contentBytes[len:]
		}
		contentsMap[nameLine] = contentBytes
		if err == io.EOF {
			return contentsMap, nil
		}
		if err != nil {
			return nil, err
		}
		for bs, err := reader.Peek(1); err == nil && bs[0] == byte('\n'); bs, err = reader.Peek(1) {
			reader.ReadByte()
		}
	}
}

// notesMapping represents the association between a git object and the notes for that object.
type notesMapping struct {
	ObjectHash *string
	NotesHash  *string
}

// notesOverview represents a high-level overview of all the notes under a single notes ref.
type notesOverview struct {
	NotesMappings      []*notesMapping
	ObjectHashesReader io.Reader
	NotesHashesReader  io.Reader
}

// notesOverview returns an overview of the git notes stored under the given ref.
func (repo *GitRepo) notesOverview(notesRef string) (*notesOverview, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := repo.runGitCommandWithIO(nil, &stdout, &stderr, "notes", "--ref", notesRef, "list"); err != nil {
		return nil, err
	}

	var notesMappings []*notesMapping
	var objHashes []*string
	var notesHashes []*string
	outScanner := bufio.NewScanner(&stdout)
	for outScanner.Scan() {
		line := outScanner.Text()
		lineParts := strings.Split(line, " ")
		if len(lineParts) != 2 {
			return nil, fmt.Errorf("Malformed output line from 'git-notes list': %q", line)
		}
		objHash := &lineParts[1]
		notesHash := &lineParts[0]
		notesMappings = append(notesMappings, &notesMapping{
			ObjectHash: objHash,
			NotesHash:  notesHash,
		})
		objHashes = append(objHashes, objHash)
		notesHashes = append(notesHashes, notesHash)
	}
	err := outScanner.Err()
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("Failure parsing the output of 'git-notes list': %v", err)
	}
	return &notesOverview{
		NotesMappings:      notesMappings,
		ObjectHashesReader: stringsReader(objHashes),
		NotesHashesReader:  stringsReader(notesHashes),
	}, nil
}

// getIsCommitMap returns a mapping of all the annotated objects that are commits.
func (overview *notesOverview) getIsCommitMap(repo *GitRepo) (map[string]bool, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := repo.runGitCommandWithIO(overview.ObjectHashesReader, &stdout, &stderr, "cat-file", "--batch-check=%(objectname) %(objecttype)"); err != nil {
		return nil, fmt.Errorf("Failure performing a batch file check: %v", err)
	}
	isCommit, err := splitBatchCheckOutput(&stdout)
	if err != nil {
		return nil, fmt.Errorf("Failure parsing the output of a batch file check: %v", err)
	}
	return isCommit, nil
}

// getNoteContentsMap returns a mapping from all the notes hashes to their contents.
func (overview *notesOverview) getNoteContentsMap(repo *GitRepo) (map[string][]byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := repo.runGitCommandWithIO(overview.NotesHashesReader, &stdout, &stderr, "cat-file", "--batch=%(objectname)\n%(objectsize)"); err != nil {
		return nil, fmt.Errorf("Failure performing a batch file read: %v", err)
	}
	noteContentsMap, err := splitBatchCatFileOutput(&stdout)
	if err != nil {
		return nil, fmt.Errorf("Failure parsing the output of a batch file read: %v", err)
	}
	return noteContentsMap, nil
}

// GetAllNotes reads the contents of the notes under the given ref for every commit.
//
// The returned value is a mapping from commit hash to the list of notes for that commit.
//
// This is the batch version of the corresponding GetNotes(...) method.
func (repo *GitRepo) GetAllNotes(notesRef string) (map[string][]Note, error) {
	// This code is unfortunately quite complicated, but it needs to be so.
	//
	// Conceptually, this is equivalent to:
	//   result := make(map[string][]Note)
	//   for _, commit := range repo.ListNotedRevisions(notesRef) {
	//     result[commit] = repo.GetNotes(notesRef, commit)
	//   }
	//   return result, nil
	//
	// However, that logic would require separate executions of the 'git'
	// command for every annotated commit. For a repo with 10s of thousands
	// of reviews, that would mean calling Cmd.Run(...) 10s of thousands of
	// times. That, in turn, would take so long that the tool would be unusable.
	//
	// This method avoids that by taking advantage of the 'git cat-file --batch="..."'
	// command. That allows us to use a single invocation of Cmd.Run(...) to
	// inspect multiple git objects at once.
	//
	// As such, regardless of the number of reviews in a repo, we can get all
	// of the notes using a total of three invocations of Cmd.Run(...):
	//  1. One to list all the annotated objects (and their notes hash)
	//  2. A second one to filter out all of the annotated objects that are not commits.
	//  3. A final one to get the contents of all of the notes blobs.
	overview, err := repo.notesOverview(notesRef)
	if err != nil {
		return nil, err
	}
	isCommit, err := overview.getIsCommitMap(repo)
	if err != nil {
		return nil, fmt.Errorf("Failure building the set of commit objects: %v", err)
	}
	noteContentsMap, err := overview.getNoteContentsMap(repo)
	if err != nil {
		return nil, fmt.Errorf("Failure building the mapping from notes hash to contents: %v", err)
	}
	commitNotesMap := make(map[string][]Note)
	for _, notesMapping := range overview.NotesMappings {
		if !isCommit[*notesMapping.ObjectHash] {
			continue
		}
		noteBytes := noteContentsMap[*notesMapping.NotesHash]
		byteSlices := bytes.Split(noteBytes, []byte("\n"))
		var notes []Note
		for _, slice := range byteSlices {
			notes = append(notes, Note(slice))
		}
		commitNotesMap[*notesMapping.ObjectHash] = notes
	}

	return commitNotesMap, nil
}

// AppendNote appends a note to a revision under the given ref.
func (repo *GitRepo) AppendNote(notesRef, revision string, note Note) error {
	_, err := repo.runGitCommand("notes", "--ref", notesRef, "append", "-m", string(note), revision)
	return err
}

// ListNotedRevisions returns the collection of revisions that are annotated by notes in the given ref.
func (repo *GitRepo) ListNotedRevisions(notesRef string) []string {
	var revisions []string
	notesListOut, err := repo.runGitCommand("notes", "--ref", notesRef, "list")
	if err != nil {
		return nil
	}
	notesList := strings.Split(notesListOut, "\n")
	for _, notePair := range notesList {
		noteParts := strings.SplitN(notePair, " ", 2)
		if len(noteParts) == 2 {
			objHash := noteParts[1]
			objType, err := repo.runGitCommand("cat-file", "-t", objHash)
			// If a note points to an object that we do not know about (yet), then err will not
			// be nil. We can safely just ignore those notes.
			if err == nil && objType == "commit" {
				revisions = append(revisions, objHash)
			}
		}
	}
	return revisions
}

// Remotes returns a list of the remotes.
func (repo *GitRepo) Remotes() ([]string, error) {
	remotes, err := repo.runGitCommand("remote")
	if err != nil {
		return nil, err
	}
	remoteNames := strings.Split(remotes, "\n")
	var result []string
	for _, name := range remoteNames {
		result = append(result, strings.TrimSpace(name))
	}
	sort.Strings(result)
	return result, nil
}

// Fetch fetches from the given remote using the supplied refspecs.
func (repo *GitRepo) Fetch(remote string, refspecs ...string) error {
	args := []string{"fetch", remote}
	args = append(args, refspecs...)
	return repo.runGitCommandInline(args...)
}

// PushNotes pushes git notes to a remote repo.
func (repo *GitRepo) PushNotes(remote, notesRefPattern string) error {
	refspec := fmt.Sprintf("%s:%s", notesRefPattern, notesRefPattern)

	// The push is liable to fail if the user forgot to do a pull first, so
	// we treat errors as user errors rather than fatal errors.
	err := repo.runGitCommandInline("push", remote, refspec)
	if err != nil {
		return fmt.Errorf("Failed to push to the remote '%s': %v", remote, err)
	}
	return nil
}

// PushNotesAndArchive pushes the given notes and archive refs to a remote repo.
func (repo *GitRepo) PushNotesAndArchive(remote, notesRefPattern, archiveRefPattern string) error {
	notesRefspec := fmt.Sprintf("%s:%s", notesRefPattern, notesRefPattern)
	archiveRefspec := fmt.Sprintf("%s:%s", archiveRefPattern, archiveRefPattern)
	err := repo.runGitCommandInline("push", remote, notesRefspec, archiveRefspec)
	if err != nil {
		return fmt.Errorf("Failed to push the local archive to the remote '%s': %v", remote, err)
	}
	return nil
}

func (repo *GitRepo) getRefHashes(refPattern string) (map[string]string, error) {
	if !strings.HasSuffix(refPattern, "/*") {
		return nil, fmt.Errorf("unsupported ref pattern %q", refPattern)
	}
	refPrefix := strings.TrimSuffix(refPattern, "*")
	showRef, err := repo.runGitCommand("show-ref")
	if err != nil {
		return nil, err
	}
	refsMap := make(map[string]string)
	for _, line := range strings.Split(showRef, "\n") {
		lineParts := strings.Split(line, " ")
		if len(lineParts) != 2 {
			return nil, fmt.Errorf("unexpected line in output of `git show-ref`: %q", line)
		}
		if strings.HasPrefix(lineParts[1], refPrefix) {
			refsMap[lineParts[1]] = lineParts[0]
		}
	}
	return refsMap, nil
}

func getRemoteNotesRef(remote, localNotesRef string) string {
	// Note: The pattern for remote notes deviates from that of remote heads and devtools,
	// because the git command line tool requires all notes refs to be located under the
	// "refs/notes/" prefix.
	//
	// Because of that, we make the remote refs a subset of the local refs instead of
	// a parallel tree, which is the pattern used for heads and devtools.
	//
	// E.G. ("refs/notes/..." -> "refs/notes/remotes/<remote>/...")
	//   versus ("refs/heads/..." -> "refs/remotes/<remote>/...")
	relativeNotesRef := strings.TrimPrefix(localNotesRef, notesRefPrefix)
	return notesRefPrefix + "remotes/" + remote + "/" + relativeNotesRef
}

func getLocalNotesRef(remote, remoteNotesRef string) string {
	relativeNotesRef := strings.TrimPrefix(remoteNotesRef, notesRefPrefix+"remotes/"+remote+"/")
	return notesRefPrefix + relativeNotesRef
}

// MergeNotes merges in the remote's state of the notes reference into the
// local repository's.
func (repo *GitRepo) MergeNotes(remote, notesRefPattern string) error {
	remoteRefPattern := getRemoteNotesRef(remote, notesRefPattern)
	refsMap, err := repo.getRefHashes(remoteRefPattern)
	if err != nil {
		return err
	}
	for remoteRef := range refsMap {
		localRef := getLocalNotesRef(remote, remoteRef)
		if _, err := repo.runGitCommand("notes", "--ref", localRef, "merge", remoteRef, "-s", "cat_sort_uniq"); err != nil {
			return err
		}
	}
	return nil
}

// PullNotes fetches the contents of the given notes ref from a remote repo,
// and then merges them with the corresponding local notes using the
// "cat_sort_uniq" strategy.
func (repo *GitRepo) PullNotes(remote, notesRefPattern string) error {
	remoteNotesRefPattern := getRemoteNotesRef(remote, notesRefPattern)
	fetchRefSpec := fmt.Sprintf("+%s:%s", notesRefPattern, remoteNotesRefPattern)
	err := repo.Fetch(remote, fetchRefSpec)
	if err != nil {
		return err
	}

	return repo.MergeNotes(remote, notesRefPattern)
}

func getRemoteDevtoolsRef(remote, devtoolsRefPattern string) string {
	relativeRef := strings.TrimPrefix(devtoolsRefPattern, devtoolsRefPrefix)
	return remoteDevtoolsRefPrefix + remote + "/" + relativeRef
}

func getLocalDevtoolsRef(remote, remoteDevtoolsRef string) string {
	relativeRef := strings.TrimPrefix(remoteDevtoolsRef, remoteDevtoolsRefPrefix+remote+"/")
	return devtoolsRefPrefix + relativeRef
}

// MergeArchives merges in the remote's state of the archives reference into
// the local repository's.
func (repo *GitRepo) MergeArchives(remote, archiveRefPattern string) error {
	remoteRefPattern := getRemoteDevtoolsRef(remote, archiveRefPattern)
	refsMap, err := repo.getRefHashes(remoteRefPattern)
	if err != nil {
		return err
	}
	for remoteRef := range refsMap {
		localRef := getLocalDevtoolsRef(remote, remoteRef)
		if err := repo.mergeArchives(localRef, remoteRef); err != nil {
			return err
		}
	}
	return nil
}

// FetchAndReturnNewReviewHashes fetches the notes "branches" and then susses
// out the IDs (the revision the review points to) of any new reviews, then
// returns that list of IDs.
//
// This is accomplished by determining which files in the notes tree have
// changed because the _names_ of these files correspond to the revisions they
// point to.
func (repo *GitRepo) FetchAndReturnNewReviewHashes(remote, notesRefPattern string, devtoolsRefPatterns ...string) ([]string, error) {
	for _, refPattern := range devtoolsRefPatterns {
		if !strings.HasPrefix(refPattern, devtoolsRefPrefix) {
			return nil, fmt.Errorf("Unsupported devtools ref: %q", refPattern)
		}
	}
	remoteNotesRefPattern := getRemoteNotesRef(remote, notesRefPattern)
	notesFetchRefSpec := fmt.Sprintf("+%s:%s", notesRefPattern, remoteNotesRefPattern)

	localDevtoolsRefPattern := devtoolsRefPrefix + "*"
	remoteDevtoolsRefPattern := getRemoteDevtoolsRef(remote, localDevtoolsRefPattern)
	devtoolsFetchRefSpec := fmt.Sprintf("+%s:%s", localDevtoolsRefPattern, remoteDevtoolsRefPattern)

	// Prior to fetching, record the current state of the remote notes refs
	priorRefHashes, err := repo.getRefHashes(remoteNotesRefPattern)
	if err != nil {
		return nil, fmt.Errorf("failure reading the existing ref hashes for the remote %q: %v", remote, err)
	}

	if err := repo.Fetch(remote, notesFetchRefSpec, devtoolsFetchRefSpec); err != nil {
		return nil, fmt.Errorf("failure fetching from the remote %q: %v", remote, err)
	}

	// After fetching, record the updated state of the remote notes refs
	updatedRefHashes, err := repo.getRefHashes(remoteNotesRefPattern)
	if err != nil {
		return nil, fmt.Errorf("failure reading the updated ref hashes for the remote %q: %v", remote, err)
	}

	// Now that we have our two lists, we need to merge them.
	updatedReviewSet := make(map[string]struct{})
	for ref, hash := range updatedRefHashes {
		priorHash, ok := priorRefHashes[ref]
		if priorHash == hash {
			// Nothing has changed for this ref
			continue
		}
		var notes string
		var err error
		if !ok {
			// This is a new ref, so include every noted object
			notes, err = repo.runGitCommand("ls-tree", "-r", "--name-only", hash)
		} else {
			notes, err = repo.runGitCommand("diff", "--name-only", priorHash, hash)
		}
		if err != nil {
			return nil, err
		}
		// The name of the review matches the name of the notes tree entry, with slashes removed
		reviews := strings.Split(strings.Replace(notes, "/", "", -1), "\n")
		for _, review := range reviews {
			updatedReviewSet[review] = struct{}{}
		}
	}

	updatedReviews := make([]string, 0, len(updatedReviewSet))
	for key, _ := range updatedReviewSet {
		updatedReviews = append(updatedReviews, key)
	}
	return updatedReviews, nil
}

// PullNotesAndArchive fetches the contents of the notes and archives refs from
// a remote repo, and merges them with the corresponding local refs.
//
// For notes refs, we assume that every note can be automatically merged using
// the 'cat_sort_uniq' strategy (the git-appraise schemas fit that requirement),
// so we automatically merge the remote notes into the local notes.
//
// For "archive" refs, they are expected to be used solely for maintaining
// reachability of commits that are part of the history of any reviews,
// so we do not maintain any consistency with their tree objects. Instead,
// we merely ensure that their history graph includes every commit that we
// intend to keep.
func (repo *GitRepo) PullNotesAndArchive(remote, notesRefPattern, archiveRefPattern string) error {
	if _, err := repo.FetchAndReturnNewReviewHashes(remote, notesRefPattern, archiveRefPattern); err != nil {
		return fmt.Errorf("failure fetching from the remote %q: %v", remote, err)
	}
	if err := repo.MergeArchives(remote, archiveRefPattern); err != nil {
		return fmt.Errorf("failure merging archives from the remote %q: %v", remote, err)
	}
	if err := repo.MergeNotes(remote, notesRefPattern); err != nil {
		return fmt.Errorf("failure merging notes from the remote %q: %v", remote, err)
	}
	return nil
}

// Push pushes the given refs to a remote repo.
func (repo *GitRepo) Push(remote string, refSpecs ...string) error {
	pushArgs := append([]string{"push", remote}, refSpecs...)
	err := repo.runGitCommandInline(pushArgs...)
	if err != nil {
		return fmt.Errorf("Failed to push the local refs to the remote '%s': %v", remote, err)
	}
	return nil
}

package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/KoviRobi/git-appraise/commands/web"
	"github.com/KoviRobi/git-appraise/repository"
)

var port = flag.Uint("port", 0, "Web server port.")

//go:embed repos.html
var repos_html string

type ServeMultiPaths struct {}

func (ServeMultiPaths) Css() string { return "/stylesheet.css" }
func (ServeMultiPaths) Repo() string { return "repo.html" }
func (ServeMultiPaths) Branch(branch uint64) string {
	return fmt.Sprintf("branch.html?branch=%d", branch)
}
func (ServeMultiPaths) Review(branch uint64, review string) string {
	return fmt.Sprintf("review.html?branch=%d&review=%s", branch, review)
}


type Repos map[string]*web.RepoDetails

func (repos Repos) Discover() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			path, err = filepath.Rel(cwd, path)
			if err != nil {
				return nil
			}
			gitRepo, err := repository.NewGitRepo(path)
			if err != nil {
				return nil
			}
			repoDetails, err := web.NewRepoDetails(gitRepo)
			if err != nil {
				return nil
			}
			if err := repoDetails.Update(); err != nil {
				return nil
			}
			repos[path] = repoDetails
			return filepath.SkipDir
		}
		return nil
	})
}

func (repos Repos) ServeStyleSheet(w http.ResponseWriter, r *http.Request) {
	web.ServeStyleSheet(w, r)
}

func (repos Repos) ServeRepoTemplate(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	if repoDetails, found := repos[repo]; found {
		repoDetails.ServeRepoTemplateWith(ServeMultiPaths{}, w, r)
	} else {
		http.Error(w, "Repository " + repo + " not found!", http.StatusNotFound)
	}
}

func (repos Repos) ServeBranchTemplate(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	if repoDetails, found := repos[repo]; found {
		repoDetails.ServeBranchTemplateWith(ServeMultiPaths{}, w, r)
	} else {
		http.Error(w, "Repository " + repo + " not found!", http.StatusNotFound)
	}
}

func (repos Repos) ServeReviewTemplate(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	if repoDetails, found := repos[repo]; found {
		repoDetails.ServeReviewTemplateWith(ServeMultiPaths{}, w, r)
	} else {
		http.Error(w, "Repository " + repo + " not found!", http.StatusNotFound)
	}
}

func (repos Repos) ServeReposTemplate(w http.ResponseWriter, r *http.Request) {
	type ReposInfo struct {
		Repos Repos
		GitWeb string
	}
	reposInfo := ReposInfo {
		Repos: repos,
		GitWeb: "/gitweb",
	}
	var writer bytes.Buffer
	if err := web.ServeTemplate(reposInfo, ServeMultiPaths{}, &writer, "repos", repos_html); err != nil {
		web.ServeErrorTemplate(err, http.StatusInternalServerError, w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(writer.Bytes())
}

func (repos *Repos) ServeEntryPointRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/repos.html", http.StatusTemporaryRedirect)
	return
}

func webServe() {
	var paths ServeMultiPaths
	repos := make(Repos)

	repos.Discover()

	http.HandleFunc("/_ah/health",
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "ok")
		})

	stylesheet, _, _ := strings.Cut(paths.Css(), "?")
	repo, _, _       := strings.Cut(paths.Repo(), "?")
	branch, _, _     := strings.Cut(paths.Branch(0), "?")
	review, _, _     := strings.Cut(paths.Review(0, ""), "?")

	http.HandleFunc("/repos.html", repos.ServeReposTemplate)
	http.HandleFunc(stylesheet, repos.ServeStyleSheet)
	http.HandleFunc("/{repo}/" + repo, repos.ServeRepoTemplate)
	http.HandleFunc("/{repo}/" + branch, repos.ServeBranchTemplate)
	http.HandleFunc("/{repo}/" + review, repos.ServeReviewTemplate)
	http.HandleFunc("/", repos.ServeEntryPointRedirect)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		fmt.Printf("Error: %#v\n", err)
	}
}

func main() {
	flag.Parse()
	webServe()
}

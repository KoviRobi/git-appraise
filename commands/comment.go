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

package commands

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/KoviRobi/git-appraise/commands/input"
	"github.com/KoviRobi/git-appraise/repository"
	"github.com/KoviRobi/git-appraise/review"
	"github.com/KoviRobi/git-appraise/review/comment"
	"github.com/KoviRobi/git-appraise/review/gpg"
)

var commentFlagSet = flag.NewFlagSet("comment", flag.ExitOnError)
var commentLocation = comment.Range{}

var (
	commentMessageFile = commentFlagSet.String("F", "", "Take the comment from the given file. Use - to read the message from the standard input")
	commentMessage     = commentFlagSet.String("m", "", "Message body of the comment")
	commentParent      = commentFlagSet.String("p", "", "Parent comment")
	commentFile        = commentFlagSet.String("f", "", "File being commented upon")
	commentDetached    = commentFlagSet.Bool("d", false, "Do not attach the comment to a review")
	commentLgtm        = commentFlagSet.Bool("lgtm", false, "'Looks Good To Me'. Set this to express your approval. This cannot be combined with nmw")
	commentNmw         = commentFlagSet.Bool("nmw", false, "'Needs More Work'. Set this to express your disapproval. This cannot be combined with lgtm")
	commentSign        = commentFlagSet.Bool("S", false, "Sign the contents of the comment")
	commentDate        = commentFlagSet.String("date", "", "comment date")
)

func init() {
	commentFlagSet.Var(&commentLocation, "l",
		`File location to be commented upon; requires that the -f flag also be set.
Location follows the following format:
    <START LINE>[+<START COLUMN>][:<END LINE>[+<END COLUMN>]]
So, in order to comment starting on the 5th character of the 2nd line until (and
including) the 4th character of the 7th line, use:
    -l 2+5:7+4`)
}

// commentHashExists checks if the given comment hash exists in the given comment threads.
func commentHashExists(hashToFind string, threads []review.CommentThread) bool {
	for _, thread := range threads {
		if thread.Hash == hashToFind {
			return true
		}
		if commentHashExists(hashToFind, thread.Children) {
			return true
		}
	}
	return false
}

func validateArgs(repo repository.Repo, args []string, threads []review.CommentThread) error {
	if *commentLgtm && *commentNmw {
		return errors.New("You cannot combine the flags -lgtm and -nmw.")
	}
	if *commentParent != "" && !commentHashExists(*commentParent, threads) {
		return errors.New("There is no matching parent comment.")
	}

	if *commentMessageFile != "" && *commentMessage == "" {
		var err error
		*commentMessage, err = input.FromFile(*commentMessageFile)
		if err != nil {
			return err
		}
	}
	if *commentMessageFile == "" && *commentMessage == "" {
		var err error
		*commentMessage, err = input.LaunchEditor(repo, commentFilename)
		if err != nil {
			return err
		}
	}
	if *commentMessageFile == "" && *commentMessage == "" {
		return errors.New("No comment")
	}
	return nil
}

func buildCommentFromFlags(repo repository.Repo, commentedUponCommit string) (*comment.Comment, error) {
	location := comment.Location{
		Commit: commentedUponCommit,
	}
	if *commentFile != "" {
		location.Path = *commentFile
	}
	location.Range = &commentLocation
	if err := location.Check(repo); err != nil {
		return nil, fmt.Errorf("Unable to comment on the given location: %v", err)
	}

	userEmail, err := repo.GetUserEmail()
	if err != nil {
		return nil, err
	}

	date, err := GetDate(*commentDate)
	if err != nil {
		return nil, err
	}
	if date == nil {
		now := time.Now()
		date = &now
	}
	timestamp := FormatDate(date)
	c := comment.New(userEmail, *commentMessage)
	c.Location = &location
	c.Parent = *commentParent
	if len(timestamp) > 0 {
		c.Timestamp = timestamp
	}
	if *commentLgtm || *commentNmw {
		resolved := *commentLgtm
		c.Resolved = &resolved
	}

	if *commentSign {
		key, err := repo.GetUserSigningKey()
		if err != nil {
			return nil, err
		}
		err = gpg.Sign(key, &c)
		if err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// commentOnReview adds a comment to the current code review.
func commentOnReview(repo repository.Repo, args []string) error {
	var r *review.Review
	var err error

	if len(args) > 1 {
		return errors.New("Only commenting on a single review is supported.")
	}
	if len(args) == 1 {
		r, err = review.Get(repo, args[0])
	} else {
		r, err = review.GetCurrent(repo)
	}

	if err != nil {
		return fmt.Errorf("Failed to load the review: %v\n", err)
	}
	if r == nil {
		return errors.New("There is no matching review.")
	}

	if err := validateArgs(repo, args, r.Comments); err != nil {
		return err
	}

	commentedUponCommit, err := r.GetHeadCommit()
	if err != nil {
		return err
	}

	c, err := buildCommentFromFlags(r.Repo, commentedUponCommit)
	if err != nil {
		return err
	}
	return r.AddComment(*c)
}

// commentOnPath adds a comment about the given file without attaching it to a review.
func commentOnPath(repo repository.Repo, args []string) error {
	if *commentFile == "" {
		return errors.New("You must specify the containing file for detached comments.")
	}

	if len(args) > 1 {
		return errors.New("Only commenting on a single location is supported.")
	}
	var commentedUponRef string
	if len(args) == 1 {
		commentedUponRef = args[0]
	} else {
		commentedUponRef = "HEAD"
	}
	commentedUponCommit, err := repo.ResolveRefCommit(commentedUponRef)
	if err != nil {
		return fmt.Errorf("Failed to resolve the comment location: %v\n", err)
	}

	commentThreads, err := review.GetDetachedComments(repo, *commentFile)
	if err != nil {
		return err
	}
	if err := validateArgs(repo, args, commentThreads); err != nil {
		return err
	}

	c, err := buildCommentFromFlags(repo, commentedUponCommit)
	if err != nil {
		return err
	}
	return review.AddDetachedComment(repo, c)
}

// commentCmd defines the "comment" subcommand.
var commentCmd = &Command{
	Usage: func(arg0 string) {
		fmt.Printf("Usage: %s comment [<option>...] [<review-hash>]\n\nOptions:\n", arg0)
		commentFlagSet.PrintDefaults()
	},
	RunMethod: func(repo repository.Repo, args []string) error {
		commentFlagSet.Parse(args)
		args = commentFlagSet.Args()
		if *commentDetached {
			return commentOnPath(repo, args)
		}
		return commentOnReview(repo, args)
	},
}

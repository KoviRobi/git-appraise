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

var acceptFlagSet = flag.NewFlagSet("accept", flag.ExitOnError)

var (
	acceptMessageFile = acceptFlagSet.String("F", "", "Take the comment from the given file. Use - to read the message from the standard input")
	acceptMessage     = acceptFlagSet.String("m", "", "Message to attach to the review")
	acceptDate        = acceptFlagSet.String("date", "", "Date to use for the review")
	acceptSign        = acceptFlagSet.Bool("S", false,
		"sign the contents of the acceptance")
)

// acceptReview adds an LGTM comment to the current code review.
func acceptReview(repo repository.Repo, args []string) error {
	acceptFlagSet.Parse(args)
	args = acceptFlagSet.Args()

	var r *review.Review
	var err error
	if len(args) > 1 {
		return errors.New("Only accepting a single review is supported.")
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

	acceptedCommit, err := r.GetHeadCommit()
	if err != nil {
		return err
	}
	location := comment.Location{
		Commit: acceptedCommit,
	}
	resolved := true
	userEmail, err := repo.GetUserEmail()
	if err != nil {
		return err
	}

	if *acceptMessageFile != "" && *acceptMessage == "" {
		*acceptMessage, err = input.FromFile(*acceptMessageFile)
		if err != nil {
			return err
		}
	}

	date, err := GetDate(*acceptDate)
	if err != nil {
		return err
	}
	if date == nil {
		now := time.Now()
		date = &now
	}
	timestamp := FormatDate(date)
	c := comment.New(userEmail, *acceptMessage)
	c.Location = &location
	c.Resolved = &resolved
	if len(timestamp) > 0 {
		c.Timestamp = timestamp
	}

	if *acceptSign {
		key, err := repo.GetUserSigningKey()
		if err != nil {
			return err
		}
		err = gpg.Sign(key, &c)
		if err != nil {
			return err
		}
	}
	return r.AddComment(c)
}

// acceptCmd defines the "accept" subcommand.
var acceptCmd = &Command{
	Usage: func(arg0 string) {
		fmt.Printf("Usage: %s accept [<option>...] [<commit>]\n\nOptions:\n", arg0)
		acceptFlagSet.PrintDefaults()
	},
	RunMethod: func(repo repository.Repo, args []string) error {
		return acceptReview(repo, args)
	},
}

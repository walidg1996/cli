package diff

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/cli/cli/api"
	"github.com/cli/cli/context"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/pr/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
)

type DiffOptions struct {
	HttpClient func() (*http.Client, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)
	Remotes    func() (context.Remotes, error)
	Branch     func() (string, error)

	SelectorArg string
	UseColor    string
}

func NewCmdDiff(f *cmdutil.Factory, runF func(*DiffOptions) error) *cobra.Command {
	opts := &DiffOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Remotes:    f.Remotes,
		Branch:     f.Branch,
	}

	cmd := &cobra.Command{
		Use:   "diff [<number> | <url> | <branch>]",
		Short: "View changes in a pull request",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			if len(args) > 0 {
				opts.SelectorArg = args[0]
			}

			if !validColorFlag(opts.UseColor) {
				return &cmdutil.FlagError{Err: fmt.Errorf("did not understand color: %q. Expected one of always, never, or auto", opts.UseColor)}
			}

			if opts.UseColor == "auto" && !opts.IO.IsStdoutTTY() {
				opts.UseColor = "never"
			}

			if runF != nil {
				return runF(opts)
			}
			return diffRun(opts)
		},
	}

	cmd.Flags().StringVar(&opts.UseColor, "color", "auto", "Use color in diff output: {always|never|auto}")

	return cmd
}

func diffRun(opts *DiffOptions) error {
	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}
	apiClient := api.NewClientFromHTTP(httpClient)

	pr, baseRepo, err := shared.PRFromArgs(apiClient, opts.BaseRepo, opts.Branch, opts.Remotes, opts.SelectorArg)
	if err != nil {
		return err
	}

	diff, err := apiClient.PullRequestDiff(baseRepo, pr.Number)
	if err != nil {
		return fmt.Errorf("could not find pull request diff: %w", err)
	}
	defer diff.Close()

	if opts.UseColor == "never" {
		_, err = io.Copy(opts.IO.Out, diff)
		return err
	}

	if opts.IO.IsStdoutTTY() {
		if pager := os.Getenv("PAGER"); pager != "" {
			return runPager(pager, diff, opts.IO.Out)
		}
	}

	diffLines := bufio.NewScanner(diff)
	for diffLines.Scan() {
		diffLine := diffLines.Text()
		switch {
		case isHeaderLine(diffLine):
			fmt.Fprintf(opts.IO.Out, "\x1b[1;38m%s\x1b[m\n", diffLine)
		case isAdditionLine(diffLine):
			fmt.Fprintf(opts.IO.Out, "\x1b[32m%s\x1b[m\n", diffLine)
		case isRemovalLine(diffLine):
			fmt.Fprintf(opts.IO.Out, "\x1b[31m%s\x1b[m\n", diffLine)
		default:
			fmt.Fprintln(opts.IO.Out, diffLine)
		}
	}

	if err := diffLines.Err(); err != nil {
		return fmt.Errorf("error reading pull request diff: %w", err)
	}

	return nil
}

var diffHeaderPrefixes = []string{"+++", "---", "diff", "index"}

func isHeaderLine(dl string) bool {
	for _, p := range diffHeaderPrefixes {
		if strings.HasPrefix(dl, p) {
			return true
		}
	}
	return false
}

func isAdditionLine(dl string) bool {
	return strings.HasPrefix(dl, "+")
}

func isRemovalLine(dl string) bool {
	return strings.HasPrefix(dl, "-")
}

func validColorFlag(c string) bool {
	return c == "auto" || c == "always" || c == "never"
}

var runPager = func(pager string, diff io.Reader, out io.Writer) error {
	args, err := shlex.Split(pager)
	if err != nil {
		return err
	}
	pagerCmd := exec.Command(args[0], args[1:]...)
	pagerCmd.Stdin = diff
	pagerCmd.Stdout = out
	return pagerCmd.Run()
}

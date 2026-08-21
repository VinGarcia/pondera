// Command pondera is the CLI over the decision engine. It is the *disciplined*
// path into the library: state lives one-decision-per-TOML-file, and every
// subcommand loads that file, applies one builder method, and saves. Because the
// builder methods enforce the ordering (criteria and weights first, Lock, then
// options and scores), the CLI cannot let you tune a weight once the options are
// in view — the anti-rationalization rule is mechanical, not a matter of habit.
//
// Usage:
//
//	pondera new          [flags] <file>   create an open decision
//	pondera add-criterion[flags] <file>   declare a weighted value (pre-lock)
//	pondera set-weight   [flags] <file>   adjust a criterion's weight (pre-lock)
//	pondera lock                 <file>   freeze the criteria and weights
//	pondera add-option   [flags] <file>   add an alternative (post-lock)
//	pondera score        [flags] <file>   score one option on one criterion
//	pondera rank                 <file>   compute and print the ranking
//	pondera show                 <file>   print the decision's current state
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/sylgarcia00/pondera"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run dispatches one subcommand. It is separated from main so tests can drive it
// with explicit args and capture output.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		usage(out)
		return fmt.Errorf("pondera: no command given")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "new":
		return cmdNew(rest)
	case "add-criterion":
		return cmdAddCriterion(rest)
	case "set-weight":
		return cmdSetWeight(rest)
	case "lock":
		return cmdLock(rest)
	case "add-option":
		return cmdAddOption(rest)
	case "score":
		return cmdScore(rest)
	case "rank":
		return cmdRank(rest, out)
	case "show":
		return cmdShow(rest, out)
	case "help", "-h", "--help":
		usage(out)
		return nil
	default:
		usage(out)
		return fmt.Errorf("pondera: unknown command %q", cmd)
	}
}

func usage(out io.Writer) {
	fmt.Fprint(out, `pondera — rank options by weighted values

Commands:
  new           --title T <file>                       create an open decision
  add-criterion --name N [--weight W] [--cost] [--absolute] <file>
  set-weight    --name N --weight W <file>             adjust a weight (pre-lock)
  lock          <file>                                 freeze criteria & weights
  add-option    --name N <file>                        add an alternative (post-lock)
  score         --option O --criterion C --value V <file>
  rank          <file>                                 print the ranking
  show          <file>                                 print current state

Discipline: weights are declared and locked before any option is added, so a
score can never bend a weight toward a favorite.
`)
}

// filePath extracts the single positional file argument left after flag parsing.
func filePath(fs *flag.FlagSet) (string, error) {
	switch fs.NArg() {
	case 0:
		return "", fmt.Errorf("pondera: missing <file> argument")
	case 1:
		return fs.Arg(0), nil
	default:
		return "", fmt.Errorf("pondera: unexpected extra arguments: %v", fs.Args()[1:])
	}
}

// edit loads the decision at path, applies fn, and saves it back only if fn
// succeeds — a rejected mutation leaves the file untouched.
func edit(path string, fn func(d *pondera.Decision) error) error {
	d, err := pondera.Load(path)
	if err != nil {
		return err
	}
	if err := fn(&d); err != nil {
		return err
	}
	return pondera.Save(path, d)
}

func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	title := fs.String("title", "", "decision title (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	if *title == "" {
		return fmt.Errorf("pondera: --title is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("pondera: %s already exists", path)
	}
	return pondera.Save(path, pondera.Decision{Title: *title})
}

func cmdAddCriterion(args []string) error {
	fs := flag.NewFlagSet("add-criterion", flag.ContinueOnError)
	name := fs.String("name", "", "criterion name (required)")
	weight := fs.Float64("weight", 1.0, "relative weight (> 0)")
	cost := fs.Bool("cost", false, "higher value is worse (subtractive)")
	absolute := fs.Bool("absolute", false, "score is a raw scalar, normalized across options")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	dir := pondera.Benefit
	if *cost {
		dir = pondera.Cost
	}
	return edit(path, func(d *pondera.Decision) error {
		return d.AddCriterion(pondera.Criterion{
			Name:      *name,
			Weight:    *weight,
			Direction: dir,
			Absolute:  *absolute,
		})
	})
}

func cmdSetWeight(args []string) error {
	fs := flag.NewFlagSet("set-weight", flag.ContinueOnError)
	name := fs.String("name", "", "criterion name (required)")
	weight := fs.Float64("weight", math.NaN(), "new weight (> 0, required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	if math.IsNaN(*weight) {
		return fmt.Errorf("pondera: --weight is required")
	}
	return edit(path, func(d *pondera.Decision) error {
		return d.SetWeight(*name, *weight)
	})
}

func cmdLock(args []string) error {
	fs := flag.NewFlagSet("lock", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	return edit(path, func(d *pondera.Decision) error {
		return d.Lock()
	})
}

func cmdAddOption(args []string) error {
	fs := flag.NewFlagSet("add-option", flag.ContinueOnError)
	name := fs.String("name", "", "option name (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	return edit(path, func(d *pondera.Decision) error {
		return d.AddOption(pondera.Option{Name: *name})
	})
}

func cmdScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	option := fs.String("option", "", "option name (required)")
	criterion := fs.String("criterion", "", "criterion name (required)")
	value := fs.Float64("value", math.NaN(), "the option's value for the criterion (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	if math.IsNaN(*value) {
		return fmt.Errorf("pondera: --value is required")
	}
	return edit(path, func(d *pondera.Decision) error {
		return d.SetScore(*option, *criterion, *value)
	})
}

func cmdRank(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rank", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	d, err := pondera.Load(path)
	if err != nil {
		return err
	}
	results, err := d.Rank()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Ranking for %q:\n", d.Title)
	width := 0
	for _, r := range results {
		if len(r.Option) > width {
			width = len(r.Option)
		}
	}
	for i, r := range results {
		fmt.Fprintf(out, "  %d. %-*s  %6.2f\n", i+1, width, r.Option, r.Score)
	}
	return nil
}

func cmdShow(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := filePath(fs)
	if err != nil {
		return err
	}
	d, err := pondera.Load(path)
	if err != nil {
		return err
	}
	state := "open (accepting criteria/weights)"
	if d.Locked() {
		state = "locked " + d.LockedAt.Format("2006-01-02 15:04") + " (accepting options/scores)"
	}
	fmt.Fprintf(out, "%s\n  state: %s\n", d.Title, state)
	fmt.Fprintln(out, "  criteria:")
	for _, c := range d.Criteria {
		mode := "bounded"
		if c.Absolute {
			mode = "absolute"
		}
		dir, _ := c.Direction.MarshalText()
		fmt.Fprintf(out, "    - %s (weight %g, %s, %s)\n", c.Name, c.Weight, dir, mode)
	}
	if len(d.Options) == 0 {
		return nil
	}
	fmt.Fprintln(out, "  options:")
	for _, o := range d.Options {
		fmt.Fprintf(out, "    - %s\n", o.Name)
		for _, c := range d.Criteria {
			if v, ok := o.Scores[c.Name]; ok {
				fmt.Fprintf(out, "        %s = %g\n", c.Name, v)
			} else {
				fmt.Fprintf(out, "        %s = (unscored)\n", c.Name)
			}
		}
	}
	return nil
}

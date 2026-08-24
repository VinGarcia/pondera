package pondera

import (
	"fmt"
	"time"
)

// The build methods enforce pondera's anti-rationalization discipline: the
// decider declares the criteria and their weights, freezes them with Lock, and
// only then adds the options. Before Lock, criteria and weights are editable but
// options are rejected; after Lock, the reverse. Weights therefore cannot move
// once the options are in view, which is the whole point — you commit to what
// you value, and how much, before you can be tempted to bend it toward a favorite.

// Locked reports whether the criteria and weights are frozen.
func (d *Decision) Locked() bool {
	return !d.LockedAt.IsZero()
}

// AddCriterion appends a criterion to an open decision. It errors if the
// decision is locked, the name is empty or already used (names key the option
// scores, so they must be unique), or the weight is not positive.
func (d *Decision) AddCriterion(c Criterion) error {
	if d.Locked() {
		return fmt.Errorf("pondera: cannot add criterion %q after lock", c.Name)
	}
	if c.Name == "" {
		return fmt.Errorf("pondera: criterion name is empty")
	}
	if c.Weight <= 0 {
		return fmt.Errorf("pondera: criterion %q has non-positive weight %g", c.Name, c.Weight)
	}
	if c.Allocation && !c.Range.isIdentity() {
		return fmt.Errorf("pondera: allocation criterion %q cannot also set a range %s", c.Name, c.Range)
	}
	if d.criterion(c.Name) != nil {
		return fmt.Errorf("pondera: criterion %q already exists", c.Name)
	}
	d.Criteria = append(d.Criteria, c)
	return nil
}

// SetWeight changes an existing criterion's weight while the decision is open.
func (d *Decision) SetWeight(name string, w float64) error {
	if d.Locked() {
		return fmt.Errorf("pondera: cannot set weight for %q after lock", name)
	}
	if w <= 0 {
		return fmt.Errorf("pondera: weight for %q must be positive, got %g", name, w)
	}
	c := d.criterion(name)
	if c == nil {
		return fmt.Errorf("pondera: no criterion named %q", name)
	}
	c.Weight = w
	return nil
}

// Lock freezes the criteria and weights, opening the decision for options. It
// errors if already locked or if no criteria have been declared — locking an
// empty value set would defeat the discipline.
func (d *Decision) Lock() error {
	if d.Locked() {
		return fmt.Errorf("pondera: decision %q already locked", d.Title)
	}
	if len(d.Criteria) == 0 {
		return fmt.Errorf("pondera: cannot lock decision %q with no criteria", d.Title)
	}
	d.LockedAt = time.Now()
	return nil
}

// AddOption adds an option to a locked decision. It errors if the decision is
// not yet locked, the name is empty or already used, or a score names a
// criterion that does not exist (catching typos before Rank). Scores may be
// partial here and completed with SetScore; Rank enforces completeness.
func (d *Decision) AddOption(o Option) error {
	if !d.Locked() {
		return fmt.Errorf("pondera: cannot add option %q before lock", o.Name)
	}
	if o.Name == "" {
		return fmt.Errorf("pondera: option name is empty")
	}
	if d.option(o.Name) != nil {
		return fmt.Errorf("pondera: option %q already exists", o.Name)
	}
	for name := range o.Scores {
		if d.criterion(name) == nil {
			return fmt.Errorf("pondera: option %q scores unknown criterion %q", o.Name, name)
		}
	}
	d.Options = append(d.Options, o)
	return nil
}

// SetScore sets one option's value for one criterion on a locked decision.
func (d *Decision) SetScore(option string, criterion string, v float64) error {
	if !d.Locked() {
		return fmt.Errorf("pondera: cannot score option %q before lock", option)
	}
	if d.criterion(criterion) == nil {
		return fmt.Errorf("pondera: no criterion named %q", criterion)
	}
	o := d.option(option)
	if o == nil {
		return fmt.Errorf("pondera: no option named %q", option)
	}
	if o.Scores == nil {
		o.Scores = make(map[string]float64)
	}
	o.Scores[criterion] = v
	return nil
}

// criterion returns a pointer to the named criterion, or nil if absent.
func (d *Decision) criterion(name string) *Criterion {
	for i := range d.Criteria {
		if d.Criteria[i].Name == name {
			return &d.Criteria[i]
		}
	}
	return nil
}

// option returns a pointer to the named option, or nil if absent.
func (d *Decision) option(name string) *Option {
	for i := range d.Options {
		if d.Options[i].Name == name {
			return &d.Options[i]
		}
	}
	return nil
}

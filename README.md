# pondera

Rank options by weighted values: one decider, K weighted criteria, M options.

You declare *what matters* and *how much* (the criteria and their weights) and
**lock** that before you add a single option. Once locked, no score can bend a
weight toward a favorite — the anti-rationalization rule is mechanical, not a
matter of willpower. The result is an auditable ranking: a TOML file you can read,
diff, and defend.

The CLI binary is `pond` (short for pondera):

```
go install github.com/sylgarcia00/pondera/cmd/pond@latest
```

## Criteria

Each criterion has a weight (relative, `> 0`) and two independent settings:

- **direction** — `benefit` (higher is better, the default) or `--cost`
  (higher is worse; the value is inverted).
- **range** — the two anchors that turn raw values into a 0–100 contribution:
  the first anchor maps to 0, the second to 100, values between interpolate
  and values outside clamp. Each anchor is a fixed number or the keyword
  `"min"`/`"max"` (the smallest/largest value across the options):
  - `[0, 100]` (the default): the value is already a 0–100 quality score.
  - `["min", "max"]`: field-relative — the cheapest option maps to 0, the
    priciest to 100. Maximally discriminating, but it erases magnitude: two
    near-identical values still land on 0 and 100.
  - `[0, "max"]`: zero-anchored, ratio-preserving (`v/max·100`) — near-identical
    values contribute near-identically. The scale must mean "none of the
    attribute" at zero; an all-negative field contradicts the anchors and errors.
  - `[40, 80]` (or any fixed pair): rescales a custom window, e.g. a target
    scale where 40 is "floor" and 80 is "great".

  Anchors resolving to the same point (e.g. all options tie under
  `["min", "max"]`) cannot discriminate, so the criterion contributes a
  neutral 50 to every option.

The final index is `Σ(weight × value) / Σ(weight)`, on a 0–100 scale.

## Example

Ranking two cars on safety and comfort (0–100 quality) and price (raw R$
thousands, lower is better):

<!-- BEGIN VERIFIED EXAMPLE (cmd/pond/readme_test.go runs this block) -->
```console
$ pond new --title "New car" car.toml

$ pond add-criterion --name safety --weight 3 car.toml
$ pond add-criterion --name comfort --weight 2 car.toml
$ pond add-criterion --name price --weight 1.5 --cost --range min,max car.toml
$ pond lock car.toml

$ pond add-option --name Corolla car.toml
$ pond add-option --name Civic car.toml

$ pond score --option Corolla --criterion safety --value 90 car.toml
$ pond score --option Corolla --criterion comfort --value 70 car.toml
$ pond score --option Corolla --criterion price --value 130 car.toml
$ pond score --option Civic --criterion safety --value 80 car.toml
$ pond score --option Civic --criterion comfort --value 85 car.toml
$ pond score --option Civic --criterion price --value 120 car.toml

$ pond show car.toml
New car
  state: locked 2026-08-21 17:19 (accepting options/scores)
  criteria:
    - safety (weight 3, benefit, [0, 100])
    - comfort (weight 2, benefit, [0, 100])
    - price (weight 1.5, cost, ["min", "max"])
  options:
    - Corolla
        safety = 90
        comfort = 70
        price = 130
    - Civic
        safety = 80
        comfort = 85
        price = 120

$ pond rank car.toml
Ranking for "New car":
  1. Civic     86.15
  2. Corolla   63.08
```
<!-- END VERIFIED EXAMPLE -->

Civic wins even though the Corolla is safer: it is cheaper (a `["min", "max"]`
cost criterion, so R$120k beats R$130k) and more comfortable, and those outweigh the
safety edge at these weights. Change a weight before `lock` and the ranking may
flip — which is the point.

> The example above is executed verbatim by `cmd/pond/readme_test.go`; if the
> engine's output ever diverges from what is printed here, the build fails. The
> only value the test does not pin is the `locked` wall-clock stamp.

## Commands

```
pond new           --title T <file>                          create an open decision
pond add-criterion --name N [--weight W] [--cost] [--range LO,HI] <file>
pond set-weight    --name N --weight W <file>                adjust a weight (pre-lock)
pond lock          <file>                                    freeze criteria & weights
pond add-option    --name N <file>                           add an alternative (post-lock)
pond score         --option O --criterion C --value V <file>
pond rank          <file>                                    print the ranking
pond show          <file>                                    print current state
```

# pondera

Rank options by weighted values: one decider, K weighted criteria, M options.

You declare *what matters* and *how much* (the criteria and their weights) and
**lock** that before you add a single option. Once locked, no score can bend a
weight toward a favorite — the anti-rationalization rule is mechanical, not a
matter of willpower. The result is an auditable ranking: a TOML file you can read,
diff, and defend.

## Criteria

Each criterion has a weight (relative, `> 0`) and two independent flags:

- **direction** — `benefit` (higher is better, the default) or `--cost`
  (higher is worse; the value is inverted).
- **scale** — *bounded* (the default): the value is a 0–100 quality score, used
  as-is. Or `--absolute`: the value is a raw scalar (price, km, hours) and pondera
  min-max normalizes it across the options before weighting.

The final index is `Σ(weight × value) / Σ(weight)`, on a 0–100 scale.

## Example

Ranking two cars on safety and comfort (0–100 quality) and price (raw R$
thousands, lower is better):

<!-- BEGIN VERIFIED EXAMPLE (cmd/pondera/readme_test.go runs this block) -->
```console
$ pondera new --title "New car" car.toml

$ pondera add-criterion --name safety --weight 3 car.toml
$ pondera add-criterion --name comfort --weight 2 car.toml
$ pondera add-criterion --name price --weight 1.5 --cost --absolute car.toml
$ pondera lock car.toml

$ pondera add-option --name Corolla car.toml
$ pondera add-option --name Civic car.toml

$ pondera score --option Corolla --criterion safety --value 90 car.toml
$ pondera score --option Corolla --criterion comfort --value 70 car.toml
$ pondera score --option Corolla --criterion price --value 130 car.toml
$ pondera score --option Civic --criterion safety --value 80 car.toml
$ pondera score --option Civic --criterion comfort --value 85 car.toml
$ pondera score --option Civic --criterion price --value 120 car.toml

$ pondera show car.toml
New car
  state: locked 2026-08-21 17:19 (accepting options/scores)
  criteria:
    - safety (weight 3, benefit, bounded)
    - comfort (weight 2, benefit, bounded)
    - price (weight 1.5, cost, absolute)
  options:
    - Corolla
        safety = 90
        comfort = 70
        price = 130
    - Civic
        safety = 80
        comfort = 85
        price = 120

$ pondera rank car.toml
Ranking for "New car":
  1. Civic     86.15
  2. Corolla   63.08
```
<!-- END VERIFIED EXAMPLE -->

Civic wins even though the Corolla is safer: it is cheaper (an absolute cost
criterion, so R$120k beats R$130k) and more comfortable, and those outweigh the
safety edge at these weights. Change a weight before `lock` and the ranking may
flip — which is the point.

> The example above is executed verbatim by `cmd/pondera/readme_test.go`; if the
> engine's output ever diverges from what is printed here, the build fails. The
> only value the test does not pin is the `locked` wall-clock stamp.

## Commands

```
pondera new           --title T <file>                          create an open decision
pondera add-criterion --name N [--weight W] [--cost] [--absolute] <file>
pondera set-weight    --name N --weight W <file>                adjust a weight (pre-lock)
pondera lock          <file>                                    freeze criteria & weights
pondera add-option    --name N <file>                           add an alternative (post-lock)
pondera score         --option O --criterion C --value V <file>
pondera rank          <file>                                    print the ranking
pondera show          <file>                                    print current state
```

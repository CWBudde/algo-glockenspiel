# The campaign harness

A campaign is a designed comparison of optimizer configurations against this project's own fit
objective. It exists because nothing else in the repository can answer "which engine shape fits
better"; a single run tells you what one seed did once, and the smoke run in
[docs/training.md](training.md) says so in as many words. The instrument is
`cmd/glockenspiel-campaign`, a port of MayFlyCircleFit's `scripts/cmaes-measurement` onto this
objective, including the rules that harness learned the hard way.

## What a campaign is

An **arm** is one configuration under test: a backend and the knobs that distinguish it from its
neighbours. A **block** is one seed, and every arm of a design runs on every block. That is what
makes the analysis paired: the two arms of a block saw the same reference, the same budget and
the same random stream, so the difference within a block has removed everything except the
configuration. A **contrast** is a comparison the design registered before any data existed.

Jobs run in **block-major** order, so block zero runs every arm before block one starts. A
campaign stopped halfway is then a smaller campaign with every arm equally represented, rather
than a complete picture of two arms and nothing of the rest.

Arms are matched on **evaluations**, not on time or on iterations. An evaluation is one audio
render whichever backend asked for it, while an iteration means something different to each
backend and a second means something different on each machine. The cap itself is described in
[docs/optimizer.md](optimizer.md) under "Evaluation budgets".

Matching on a cap is not quite enough, because a generation is the smallest unit a population
method can abandon, so a run may overrun the cap by up to one generation and the arm with the
larger population gets the larger overrun. So the scoring rule is **cap plus trace scoring**:
`collect` scores each job at the best cost its `trace.jsonl` recorded at an evaluation count at
or below the budget. Mayfly's cut is taken at an iteration boundary rather than inside the
parallel objective, which is what keeps a capped run bit-identical at a fixed worker width. The
score a job finished with is kept beside the cap-matched one in its own CSV column, because
**cap-matched is not spend-matched** and the difference between the two numbers is worth seeing.

Jobs run in this process, one at a time, at a worker width the manifest fixes. Phase 8.4 proved
each backend reproduces its result to the bit across worker widths at a fixed seed, so pinning
the width costs nothing and keeps the elapsed column a measure of the arm rather than of the
machine's load.

## The five commands

The recipes resolve their paths against the repository root, so run them from there.

| step    | recipe                         | what it does                                                                      |
| ------- | ------------------------------ | --------------------------------------------------------------------------------- |
| plan    | `just campaign-plan DESIGN`    | writes `out/campaign/DESIGN/manifest.json`, once                                  |
| run     | `just campaign-run DESIGN`     | runs the jobs, skipping the finished ones                                         |
| status  | `just campaign-status DESIGN`  | reports progress and what is left; safe to call while a run is going              |
| collect | `just campaign-collect DESIGN` | writes `results.csv` from the run directories                                     |
| analyze | `just campaign-analyze DESIGN` | rebuilds the report from `results.csv` and writes `out/campaign/DESIGN/report.md` |

`just campaign-build` builds the one binary the recipes drive, into
`out/campaign/bin/glockenspiel-campaign`. It is a built file rather than `go run` because a
campaign is identified by the executable that planned it, and `go run` would produce a different
file between `plan` and `run`. `just campaign-smoke` removes `out/campaign/smoke` and runs the
whole sequence on the smoke design, which takes seconds.

`glockenspiel-campaign list` prints the registered designs. `run` takes `--limit N` to stop after
a number of jobs and `--only-block B` to run one block, neither of which can change a job, only
decide whether it runs now. SIGINT and SIGTERM stop it after the job in flight; the command then
exits non-zero and names the jobs that were cut, which `run` clears and repeats when the campaign
is resumed. `collect` takes `--partial` to leave out the jobs that have not run. `analyze` takes
`--dir` or a `--csv` file that was archived on its own, and `--out` to write the Markdown as well
as print it. `version` prints the build identity and the binary's SHA-256, which is what to check
before resuming a campaign planned yesterday.

### Watching a campaign

`run` prints one line per job, prefixed with its position and, once a job on this machine has
been timed, what is left: `[28/60, ~34m left] block 05 mayfly-r16 score=... elapsed=59.9s`. The
estimate is the mean of the jobs run in this process, so a slower machine reports its own speed
rather than a figure recorded somewhere else.

`status` answers the same question from outside the process, which is what a campaign redirected
to a log file needs:

```
just campaign-status engine-shape
```

It reads only files `run` has already written and writes nothing, so it is safe to call at any
time. It counts the jobs a `run` would skip -- a job a cancellation cut is pending, not finished,
by the same rule `run` resumes on -- names the job in flight, estimates the remainder, and prints
each arm's best score so far. That last column is a progress indicator and not a result: an arm
part way through its blocks has no error bar, and `analyze` is the only thing that reads a table.

## The run directory

Every job leaves one directory, written by `internal/fitrun`:

| file              | what it holds                                                                                                                                          |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `config.json`     | the full specification, the values the backend resolved, the build identity, the reference hash                                                        |
| `analysis.json`   | the reference measurement the fit was seeded and scored from                                                                                           |
| `trace.jsonl`     | one object per progress report: restart, population, evaluations, elapsed, current and best cost, and the term breakdown on a line whose best improved |
| `checkpoint.json` | the optimizer state of the search result                                                                                                               |
| `preset.json`     | the fitted preset, carrying the provenance block                                                                                                       |
| `render.wav`      | that preset rendered at the reference's length                                                                                                         |
| `result.json`     | the summary: score, terms, evaluations, iterations, restarts, stop reason, seed, workers                                                               |
| `log.txt`         | the progress log the CLI would have printed                                                                                                            |

A directory whose `result.json` reports `context_canceled` is not a finished job, whatever else
the directory holds. `run` clears it and repeats the job, and `collect` refuses the campaign
unless `--partial` says to leave it out. A job that ran for ten seconds is not comparable to one
that spent its budget, and the file's mere existence would otherwise make it look finished
forever.

A `--partial` collection cannot be analysed. `analyze` requires every arm to have a row in every
block, because a paired contrast is only paired if both arms of a block are there; `--partial` is
for looking at a campaign in flight, not for reporting on one.

The `trace.jsonl` line carries `"lambda"` right after `"restart"`. It is the population of the
run that produced the line, which is what tells a reader of an IPOP ladder which rung a cost came
from; it is zero for every backend that has no such notion.

## The manifest, and why it cannot be overwritten

`manifest.json` is written with `O_CREATE|O_EXCL` and holds the full design value, the SHA-256 of
its canonical JSON, the planning binary's path and SHA-256, the build identity (the VCS revision
and the mayfly and go-cma-es versions), the reference's path and SHA-256, the worker width, and
the job list with its seeds. `run` refuses a binary whose hash differs from the recorded one and
a reference whose content has changed. There is no override flag.

This is the fix for a specific failure. MayFlyCircleFit's `cmaes-budget-split-report.md` records
a "registration discrepancy": a secondary contrast was changed after partial results were
already visible, and nothing in the harness could tell afterwards which design the numbers
belonged to. A design that is only a value in the source is a design that can be edited between
two halves of a campaign. Stamping the design and its hash into a file that refuses to be
rewritten is what makes "the design was frozen at plan time" a fact about the run rather than a
claim about the author's intentions. Planning into a directory that already has a manifest is an
error telling you to collect the campaign or plan into a new directory.

Refusing a changed binary and a changed reference is the same argument one level down. A result
set is only a comparison if every row came out of the same objective, and the two things that
silently change it are a rebuild between jobs and a reference file edited in place.

The manifest's reference path is absolute, so a campaign directory is not portable across
machines: copied elsewhere, `run` will look for the reference where the planning machine kept it.
Plan and run a campaign on one machine; move the `results.csv`, which needs nothing else.

## results.csv

`collect` writes one row per job. The header is a contract: `analyze` refuses a file whose header
differs, because a column set that drifted is a file whose numbers mean something else. It is one
line; it is wrapped here only to fit the page.

```
design,arm,block,seed,job,engine,covariance,lambda,population,restarts_planned,budget,
score,scored_evaluations,final_score,evaluations,iterations,restarts,stop_reason,converged,
elapsed_s,pinned,dimension,matched,partial_cents,partial_level_db,partial_decay_octaves,
partial_missing,partial_extra,spectral_fine_db,spectral_coarse_db,envelope_db,decay_slope_dbps,
waveform,mayfly_version,cmaes_version,revision
```

`score` is the cap-matched score and `scored_evaluations` the evaluation count of the trace line
it came from; `final_score` and `evaluations` are what the job finished with. `covariance` and
`lambda` are empty on mayfly rows and `population` on CMA-ES rows. `restarts_planned` is the
arm's declared shape, where zero means "restart until the budget is spent" rather than "no
restarts". The `restarts` column beside it is what the optimizer reported, and it is zero on
every mayfly row: in the optimizer's vocabulary a restart is a CMA-ES cold run, and mayfly's
rounds are a schedule the wrapper drives, so `restarts_planned` is the column that carries them.
The ten term columns are the objective's breakdown of the shipped vector. The last
three columns carry the library versions and the revision, so a row can be read years later
without the directory it came from.

The whole report is rebuilt from this one file plus the registered design its rows name, which
is the point: an archived campaign is a CSV rather than a tree of gigabytes. The design supplies
the block count, the budget, the profile and the contrast family the numbers are read against, so
the CSV is enough for as long as the design stays registered under that name. The manifest is not
consulted; it stays the frozen record of what ran, which is a different question from what the
numbers say.

## What analyze reports

Three tables, in the order MayFlyCircleFit prints them, because the order is an argument.

**Table 1, arms against the control.** Mean, standard deviation, median and best of each arm's
cap-matched scores, then the paired gain against the control, its t statistic, the two-sided p,
the Holm decision and the blocks won. The gain is control minus candidate, so a positive gain
means the candidate found the lower cost. The p-values of a design's registered contrasts are
one family and are corrected by Holm's step-down at a family-wise alpha of 0.05. There is one
table per control arm, because a gain column can only be against one of them.

**Table 2, score by block.** The per-block scores with the block's winner in bold. This is what
says whether an arm won everywhere or only on average.

**Table 3, best of each arm.** The best score, the block and seed that produced it, how many
blocks landed within five percent of it, the quartiles, the mean evaluations and how much of the
budget the arm had spent when it reached the score it is scored on. This is the rare-basin
instrument: an arm whose best is far below its median found a basin the others did not, and an
arm whose blocks cluster inside the margin is reliable rather than lucky.

A design marked descriptive gets Tables 2 and 3 and no inferential statistics at all, and the
footer says so.

Two rules carried over from CircleFit constrain what may be concluded. A **mean-versus-win-count
mismatch is not acted on**: an arm with the better mean and fewer blocks won is reported as it
stands, and the resolution is more blocks rather than a choice between the two numbers. And
**absence of evidence is stated as such**: a contrast Holm retains says the design did not
separate the arms at this block count, not that the arms are equal.

## The registered designs

Designs are Go values in `internal/campaign/designs.go`, not configuration files and not flags.
A design is an argument about why a comparison is fair, and an argument belongs in reviewed
source next to the derivation of its constants. The one exception is `seed-hunt`'s `--winner`,
below.

`engine-shape` is the phase's question: does a CMA-ES arm beat the mayfly arm the project ships,
and does the shape of the restart ladder matter more than the backend.

```
engine-shape: Backend and restart shape on the C5 recording, twelve blocks of five arms at 24,000 evaluations.

12 blocks x 5 arms = 60 jobs, 24000 evaluations each, seeds 121000..121011, 12 workers

| arm | engine | shape | per-run evals | restarts planned | budget |
| --- | --- | --- | --- | --- | --- |
| mayfly-single | mayfly | desma, population 10 | 24000 | 1 | 24000 |
| mayfly-r16 | mayfly | desma, population 10, 16 rounds | 24000 | 16 | 24000 |
| sep-cmaes-r | cmaes | separable | 4800 | until budget | 24000 |
| blk-cmaes-r | cmaes | block | 4800 | until budget | 24000 |
| sep-cmaes-ipop | cmaes | separable, lambda growth 2 | 24000 | until budget | 24000 |
```

Its three contrasts are registered, not derived: `blk-cmaes-r` against `mayfly-r16` is the
primary one, `sep-cmaes-r` against `mayfly-r16` and `mayfly-single` against `mayfly-r16` are
secondary and the report says so. A **contrast family is registered rather than derived** from
the arms, because deriving it would make every added arm silently widen the correction, and a
contrast chosen after seeing the numbers is not a test of anything.

The restart shapes are expressed the way Phase 8.4 ruled. "Restart until the budget is spent" is
a per-run evaluation cap (`RunEvaluations`, 4,800, which divides the budget exactly into five
cold runs) with no restart limit, on the wrapper's own loop rather than the library's. IPOP is
`LambdaGrowth 2` on that same loop, with no per-run cap, so each run ends on Hansen's own
criteria and the next doubles the population until the budget ends the ladder. `mayfly-r16` is
one warm round plus fifteen cold restarts.

The mayfly arms also carry an iteration cap, which the CMA-ES arms do not need. Mayfly has no
evaluation budget of its own, so a run capped only on evaluations anneals as if it had thousands
of iterations left and is cut off mid-schedule. The cap is a tenth more than the budget divided by
`optimizer.MayflyEvaluationsPerIteration()`, the measured 43.05 evaluations an iteration costs at
DESMA with a population of ten under mayfly v0.7.1, so the schedules see a realistic length while
the evaluation cap stays the thing that binds. The tenth of slack is there because a cap sized
exactly leaves the last iteration half spent when the evaluation cap cuts it, and the run then
ends on its iteration cap by accident of rounding. An annealing schedule a tenth too long cools
slightly too slowly; a run that ends on its iteration cap has spent less than its budget and is
not comparable with the arm beside it.

The budget is 24,000 evaluations per job. Phase 8.4's smoke run spent about 24,000 evaluations in
60 s on twelve threads, so a job is about a minute and the sixty jobs are about an hour.

`seed-hunt` asks whether a larger initial population buys anything at a fixed budget or only buys
fewer generations. Its two arms are the winner of `engine-shape` at Hansen's default λ for the
thirty dimensions eight seeded modes encode, and the same arm at twice that λ, over forty eight
blocks. It is **descriptive**: it reports the distributions and runs no test. Which arm the
winner is cannot be known until `engine-shape` has run, so this is the one design that takes a
flag: `plan seed-hunt --winner ARM`, where the arm is a CMA-ES arm of `engine-shape`. Without
the flag the registry's own prediction, `blk-cmaes-r`, is planned.

`rounds-12k`, `rounds-24k` and `rounds-48k` are one design each, and together they are a ladder:
`mayfly-single` against `mayfly-r16` at half the campaign budget, at it, and at twice it, twelve
blocks of two arms per rung. They exist because the 2026-09-05 re-run of `engine-shape` left its
own answer confounded. `mayfly-single` won eleven blocks of twelve there, and it reached its best
at 98.8% of the budget — still improving when the cap cut it — while `mayfly-r16` had plateaued at
22.4%. A comparison in which one arm is still climbing and the other has stopped measures the cap
as much as it measures the shape.

Each rung is a design of its own rather than an arm of a wider one because `Design.Budget` is the
evaluation cap of every job in a design, and matching arms on evaluations is the property that
makes two arms comparable at all. A design cannot hold two budgets without giving that up. So each
rung is a paired test in its own right, and reading across the rungs is descriptive.

The rungs own separate seed bases for a stronger reason than the convention below. A run at half
the budget is a prefix of a run at twice it from the same seed and arm, so a shared base would
make the rungs almost perfectly correlated and a reading across them would understate its own
spread — which is the defect Phase 8.6 found in its first table, arriving by a different route.

`smoke` is four jobs of 1,200 evaluations on the short synthetic reference. It exists so the
end-to-end path is exercised by a test and by a recipe, and its numbers mean nothing. The budget
was 300 and is 1,200 because at 300 neither arm left the seeded vector, so every job scored the
same number and the smoke run rehearsed only the plumbing, never the statistics.

Each design owns a disjoint seed base — 120,000 for `smoke`, 121,000 for `engine-shape`, 122,000
for `seed-hunt`, and 123,000, 124,000 and 125,000 for the three `rounds` rungs — and a block's seed
is the base plus the block number. A test pins the
ranges apart, because two designs sharing a seed would share a search trajectory that the
analysis would read as agreement.

## Registering a new design

Add a function returning a `Design` in `internal/campaign/designs.go` and list it in
`Registered()`. Give it a seed base no other design uses, derive its budget in a comment rather
than choosing a round number, and register its contrasts explicitly with at most one marked
primary. `Design.Validate` refuses a design that could not be analysed as written: a duplicated
arm name, a contrast naming an arm that is not there, more than one primary, a non-descriptive
design with fewer than two blocks or no contrasts, and a reference that is not on disk.

Then pin it. The design tests in `internal/campaign` assert that the registry validates, that the
seed ranges stay disjoint, and that the arms of each design differ in exactly what the design
claims they differ in. Those tests are the reason a design can be trusted months later; a design
whose constants nothing pins is a design that drifts.

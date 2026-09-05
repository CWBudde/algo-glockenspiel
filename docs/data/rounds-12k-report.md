design rounds-12k: 12 blocks, budget 12000 evaluations, balanced on testdata/reference/glockenspiel_c5.wav, revision bfc780b798ee010238c69c58a57bd3f31065e843

Round schedule on the C5 recording: one long run against sixteen rounds, twelve blocks of two arms at 12000 evaluations.

### Table 1: arms against mayfly-r16

| arm           | mean     | sd       | median   | best     | gain vs mayfly-r16 | t (df=11) | p       | Holm    | blocks won |
| ------------- | -------- | -------- | -------- | -------- | ------------------ | --------- | ------- | ------- | ---------- |
| mayfly-single | 0.288232 | 0.011945 | 0.285784 | 0.269696 | +0.0508            | +15.12    | 0.00000 | reject  | 12/12      |
| mayfly-r16    | 0.339007 | 0.011012 | 0.340703 | 0.313451 | control            | control   | control | control | control    |

### Table 2: score by block

| block | seed   | mayfly-single | mayfly-r16 |
| ----- | ------ | ------------- | ---------- |
| 0     | 123000 | **0.287385**  | 0.341818   |
| 1     | 123001 | **0.274751**  | 0.343097   |
| 2     | 123002 | **0.269696**  | 0.336454   |
| 3     | 123003 | **0.295992**  | 0.353229   |
| 4     | 123004 | **0.284183**  | 0.329283   |
| 5     | 123005 | **0.280618**  | 0.337634   |
| 6     | 123006 | **0.288104**  | 0.330804   |
| 7     | 123007 | **0.296733**  | 0.340537   |
| 8     | 123008 | **0.307060**  | 0.353267   |
| 9     | 123009 | **0.283928**  | 0.313451   |
| 10    | 123010 | **0.308722**  | 0.347644   |
| 11    | 123011 | **0.281612**  | 0.340869   |

### Table 3: best of each arm

| arm           | best     | block | seed   | within 5% of best | median   | q25      | q75      | mean evaluations | spent at best |
| ------------- | -------- | ----- | ------ | ----------------- | -------- | -------- | -------- | ---------------- | ------------- |
| mayfly-single | 0.269696 | 2     | 123002 | 4/12              | 0.285784 | 0.281363 | 0.296178 | 12033            | 99.7%         |
| mayfly-r16    | 0.313451 | 9     | 123009 | 1/12              | 0.340703 | 0.335041 | 0.344234 | 12039            | 50.0%         |

Holm step-down over 1 paired contrasts at a family-wise alpha of 0.05.
the registered primary contrast is mayfly-single against mayfly-r16.

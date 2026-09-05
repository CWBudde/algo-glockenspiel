design rounds-48k: 12 blocks, budget 48000 evaluations, balanced on testdata/reference/glockenspiel_c5.wav, revision bfc780b798ee010238c69c58a57bd3f31065e843

Round schedule on the C5 recording: one long run against sixteen rounds, twelve blocks of two arms at 48000 evaluations.

### Table 1: arms against mayfly-r16

| arm           | mean     | sd       | median   | best     | gain vs mayfly-r16 | t (df=11) | p       | Holm    | blocks won |
| ------------- | -------- | -------- | -------- | -------- | ------------------ | --------- | ------- | ------- | ---------- |
| mayfly-single | 0.279863 | 0.021281 | 0.281262 | 0.249414 | +0.0226            | +3.47     | 0.00522 | reject  | 10/12      |
| mayfly-r16    | 0.302503 | 0.008634 | 0.303428 | 0.282242 | control            | control   | control | control | control    |

### Table 2: score by block

| block | seed   | mayfly-single | mayfly-r16   |
| ----- | ------ | ------------- | ------------ |
| 0     | 125000 | **0.281523**  | 0.303974     |
| 1     | 125001 | **0.295944**  | 0.302897     |
| 2     | 125002 | **0.297085**  | 0.303959     |
| 3     | 125003 | **0.281000**  | 0.298389     |
| 4     | 125004 | **0.250761**  | 0.319135     |
| 5     | 125005 | **0.252435**  | 0.306984     |
| 6     | 125006 | **0.279183**  | 0.299768     |
| 7     | 125007 | **0.266845**  | 0.282242     |
| 8     | 125008 | 0.311235      | **0.309515** |
| 9     | 125009 | 0.306221      | **0.305318** |
| 10    | 125010 | **0.286715**  | 0.299697     |
| 11    | 125011 | **0.249414**  | 0.298154     |

### Table 3: best of each arm

| arm           | best     | block | seed   | within 5% of best | median   | q25      | q75      | mean evaluations | spent at best |
| ------------- | -------- | ----- | ------ | ----------------- | -------- | -------- | -------- | ---------------- | ------------- |
| mayfly-single | 0.249414 | 11    | 125011 | 3/12              | 0.281262 | 0.263243 | 0.296229 | 48027            | 97.2%         |
| mayfly-r16    | 0.282242 | 7     | 125007 | 1/12              | 0.303428 | 0.299370 | 0.305735 | 48033            | 30.7%         |

Holm step-down over 1 paired contrasts at a family-wise alpha of 0.05.
the registered primary contrast is mayfly-single against mayfly-r16.

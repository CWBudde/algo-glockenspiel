design rounds-24k: 12 blocks, budget 24000 evaluations, balanced on testdata/reference/glockenspiel_c5.wav, revision bfc780b798ee010238c69c58a57bd3f31065e843

Round schedule on the C5 recording: one long run against sixteen rounds, twelve blocks of two arms at 24000 evaluations.

### Table 1: arms against mayfly-r16

| arm           | mean     | sd       | median   | best     | gain vs mayfly-r16 | t (df=11) | p       | Holm    | blocks won |
| ------------- | -------- | -------- | -------- | -------- | ------------------ | --------- | ------- | ------- | ---------- |
| mayfly-single | 0.272037 | 0.013052 | 0.270236 | 0.252944 | +0.0381            | +8.44     | 0.00000 | reject  | 12/12      |
| mayfly-r16    | 0.310140 | 0.005242 | 0.310663 | 0.300824 | control            | control   | control | control | control    |

### Table 2: score by block

| block | seed   | mayfly-single | mayfly-r16 |
| ----- | ------ | ------------- | ---------- |
| 0     | 124000 | **0.271125**  | 0.315204   |
| 1     | 124001 | **0.287760**  | 0.313677   |
| 2     | 124002 | **0.257892**  | 0.311321   |
| 3     | 124003 | **0.258478**  | 0.308998   |
| 4     | 124004 | **0.286877**  | 0.304697   |
| 5     | 124005 | **0.267673**  | 0.307384   |
| 6     | 124006 | **0.284596**  | 0.300824   |
| 7     | 124007 | **0.269346**  | 0.303156   |
| 8     | 124008 | **0.289132**  | 0.310004   |
| 9     | 124009 | **0.252944**  | 0.314616   |
| 10    | 124010 | **0.260199**  | 0.316965   |
| 11    | 124011 | **0.278417**  | 0.314834   |

### Table 3: best of each arm

| arm           | best     | block | seed   | within 5% of best | median   | q25      | q75      | mean evaluations | spent at best |
| ------------- | -------- | ----- | ------ | ----------------- | -------- | -------- | -------- | ---------------- | ------------- |
| mayfly-single | 0.252944 | 9     | 124009 | 4/12              | 0.270236 | 0.259769 | 0.285166 | 24003            | 98.3%         |
| mayfly-r16    | 0.300824 | 6     | 124006 | 11/12             | 0.310663 | 0.306712 | 0.314671 | 24009            | 23.5%         |

Holm step-down over 1 paired contrasts at a family-wise alpha of 0.05.
the registered primary contrast is mayfly-single against mayfly-r16.

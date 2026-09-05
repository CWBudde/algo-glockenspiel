design engine-shape: 12 blocks, budget 24000 evaluations, balanced on testdata/reference/glockenspiel_c5.wav, revision 9704ed4d6f1a2a3960bf398df7191ba1958820cb

Backend and restart shape on the C5 recording, twelve blocks of five arms at 24,000 evaluations.

### Table 1: arms against mayfly-r16

| arm | mean | sd | median | best | gain vs mayfly-r16 | t (df=11) | p | Holm | blocks won |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.287040 | 0.014399 | 0.289758 | 0.256623 | n/a | n/a | n/a | n/a | n/a |
| mayfly-r16 | 0.314557 | 0.010277 | 0.312710 | 0.296335 | control | control | control | control | control |
| sep-cmaes-r | 0.309507 | 0.007997 | 0.309171 | 0.295445 | +0.0050 | +1.70 | 0.11666 | retain | 8/12 |
| blk-cmaes-r | 0.326868 | 0.007879 | 0.326205 | 0.316289 | -0.0123 | -2.66 | 0.02237 | reject | 4/12 |
| sep-cmaes-ipop | 0.310222 | 0.014066 | 0.308432 | 0.292343 | n/a | n/a | n/a | n/a | n/a |

### Table 1: arms against mayfly-single

| arm | mean | sd | median | best | gain vs mayfly-single | t (df=11) | p | Holm | blocks won |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.287040 | 0.014399 | 0.289758 | 0.256623 | control | control | control | control | control |
| mayfly-r16 | 0.314557 | 0.010277 | 0.312710 | 0.296335 | -0.0275 | -5.18 | 0.00031 | reject | 1/12 |
| sep-cmaes-r | 0.309507 | 0.007997 | 0.309171 | 0.295445 | n/a | n/a | n/a | n/a | n/a |
| blk-cmaes-r | 0.326868 | 0.007879 | 0.326205 | 0.316289 | n/a | n/a | n/a | n/a | n/a |
| sep-cmaes-ipop | 0.310222 | 0.014066 | 0.308432 | 0.292343 | n/a | n/a | n/a | n/a | n/a |

### Table 2: score by block

| block | seed | mayfly-single | mayfly-r16 | sep-cmaes-r | blk-cmaes-r | sep-cmaes-ipop |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | 121000 | **0.288309** | 0.311931 | 0.314436 | 0.333773 | 0.297357 |
| 1 | 121001 | **0.299714** | 0.307505 | 0.307112 | 0.321555 | 0.305246 |
| 2 | 121002 | **0.279962** | 0.313489 | 0.295445 | 0.329380 | 0.320176 |
| 3 | 121003 | **0.275050** | 0.329448 | 0.304656 | 0.320295 | 0.307349 |
| 4 | 121004 | **0.301700** | 0.314636 | 0.311872 | 0.327021 | 0.302888 |
| 5 | 121005 | **0.277164** | 0.325452 | 0.318151 | 0.316338 | 0.309515 |
| 6 | 121006 | **0.291207** | 0.309518 | 0.306692 | 0.338323 | 0.320791 |
| 7 | 121007 | 0.307281 | 0.304765 | **0.300485** | 0.340390 | 0.316396 |
| 8 | 121008 | **0.276425** | 0.327112 | 0.310633 | 0.316289 | 0.294112 |
| 9 | 121009 | **0.294242** | 0.325563 | 0.326267 | 0.325389 | 0.313314 |
| 10 | 121010 | **0.256623** | 0.296335 | 0.309017 | 0.323797 | 0.343175 |
| 11 | 121011 | 0.296802 | 0.308935 | 0.309325 | 0.329865 | **0.292343** |

### Table 3: best of each arm

| arm | best | block | seed | within 5% of best | median | q25 | q75 | mean evaluations | spent at best |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mayfly-single | 0.256623 | 10 | 121010 | 1/12 | 0.289758 | 0.276979 | 0.297530 | 24003 | 99.8% |
| mayfly-r16 | 0.296335 | 10 | 121010 | 5/12 | 0.312710 | 0.308578 | 0.325480 | 24009 | 22.4% |
| sep-cmaes-r | 0.295445 | 2 | 121002 | 7/12 | 0.309171 | 0.306183 | 0.312513 | 24000 | 54.5% |
| blk-cmaes-r | 0.316289 | 8 | 121008 | 9/12 | 0.326205 | 0.321240 | 0.330842 | 24000 | 60.6% |
| sep-cmaes-ipop | 0.292343 | 11 | 121011 | 5/12 | 0.308432 | 0.301505 | 0.317341 | 24000 | 84.5% |

Holm step-down over 3 paired contrasts at a family-wise alpha of 0.05.
the registered primary contrast is blk-cmaes-r against mayfly-r16.

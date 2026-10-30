# Tested and rejected

Models tried as alternatives to Phonon and not adopted.

## Kokoro 82M (rejected, 30 Oct 2026)

- **Model:** `mlx-community/Kokoro-82M-bf16` (from `hexgrad/Kokoro-82M` v1.0, Apache 2.0, 327 MB), run through mlx-audio 0.6.3 on an M4 with 24 GB.
- **Test:** the same short briefing in English (af_heart, bm_george), French (ff_siwis) and Brazilian Portuguese (pm_alex, pf_dora), each beside Phonon's Hugh, Bryony, Augustin, Coralie, Davi and Manuela-Lu.
- **Measured:** 1.9 to 2.3 s per 10 s clip in French and Portuguese, 5 to 5.7 s in English (it loads an English language toolkit first). Process memory was 447 to 784 MB, and free memory fell by 15 to 22 points. Phonon took 0.9 to 2.1 s, used 125 to 298 MB, and free memory fell by 0 to 1 point.
- **Why rejected:** it sounded the worst of the models compared by ear, and it is slower and heavier than Phonon. It has one French voice and three ungraded Portuguese voices, and French and Portuguese are pronounced by espeak-ng rules.

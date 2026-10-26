# Test fixtures

Real tensors, so the speed tests check tts-axi's arithmetic against Gradium's
own export rather than against numbers typed by hand.

- `conditioners.safetensors`: the `duration_delta` and `num_speakers`
  conditioner weights (BF16) of `gradium/phonon-31m-en` at revision
  `8acf8881a96315724c72ed3aad2a0900d1b230ba` (Apache-2.0), read by HTTP range
  from its `model.safetensors`.
- `host-conditions.safetensors`: the `flow_lm.conditions` vector (F32) from
  `host.safetensors` in the `phonon-31m-en-coreml.zip` bundle of the
  `gradium-ai/xn-ptts` v0.4.0 release, exported from that revision at
  duration_delta 0.
- `ollama/`: a real subset of Ollama's `phonon:40m-multilingual` (CC BY-NC 4.0
  for the model, Apache-2.0 for the voices). `manifest.json` is the manifest
  from `registry.ollama.ai/v2/library/phonon/manifests/40m-multilingual` cut
  down to five tensor layers (BF16 and F32), the `voices.Davi` and
  `voices.default` tensors, the three JSON layers and both licence layers;
  `blobs/` holds those layers' blobs, named by their SHA-256.

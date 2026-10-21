package engine

import _ "embed"

// The helpers' sources are compiled into the tts-axi binary with //go:embed
// and written out when they are needed, so an installed tts-axi needs no
// files next to it.

// SwiftHelper is the Core ML helper's source, built by `tts-axi doctor --fix`.
//
//go:embed swift/main.swift
var SwiftHelper []byte

// PttsHelper is the multilingual helper, run by uv in a throwaway environment.
//
//go:embed ptts/helper.py
var PttsHelper []byte

// Release pins Gradium's runtime and model bundle. Sizes and SHA-256 sums were
// recorded from the v0.4.0 release assets; a download that differs is refused.
const Release = "v0.4.0"

type asset struct {
	Name   string
	Size   int64
	SHA256 string
}

var (
	frameworkAsset = asset{"PhononCore.xcframework.zip", 39007317, "a10382948448087bc9f63231de3c2970d606979fd69bef7acb95d2dfa3a41cef"}
	wrapperAsset   = asset{"ptts-swift-0.4.0.zip", 16866, "979ea983227d1f6b0c2e3b465b52d152830f9e6bd9369b7846a6253256dde105"}
	bundleAsset    = asset{"phonon-31m-en-coreml.zip", 137096081, "10f3a99fd39256db44f826267ef21d78cca959b16c0554926344c09fde27b436"}
)

// BundleName is the English Core ML bundle's folder name.
const BundleName = "phonon-31m-en-coreml"

// Checkpoint is the model the English bundle was exported from; its
// duration_delta conditioner is read from here to set the speed.
const Checkpoint = "https://huggingface.co/gradium/phonon-31m-en/resolve/8acf8881a96315724c72ed3aad2a0900d1b230ba/model.safetensors"

// MultilingualModel speaks French and Portuguese through ptts on the CPU.
const MultilingualModel = "hf://gradium/phonon-40m-multilingual"

// PttsVersion is the Python runtime uv installs for the multilingual route.
const PttsVersion = "0.4.1"

func releaseURL(name string) string {
	return "https://github.com/gradium-ai/xn-ptts/releases/download/" + Release + "/" + name
}

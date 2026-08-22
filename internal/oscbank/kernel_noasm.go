//go:build !amd64 && !arm64

package oscbank

func processRotorBlocks(re, im, cosCoeff, sinCoeff, amp []float32, blocks int, input, acc []float32) {
	processRotorBlocksGeneric(re, im, cosCoeff, sinCoeff, amp, blocks, input, acc)
}

func processVoiceRotors(re, im, cosCoeff, sinCoeff, amp []float32, rotors int, input, acc []float32) {
	processVoiceRotorsGeneric(re, im, cosCoeff, sinCoeff, amp, rotors, input, acc)
}

func reduceLanes(acc, output []float32) {
	reduceLanesGeneric(acc, output)
}

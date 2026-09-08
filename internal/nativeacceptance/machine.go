package nativeacceptance

import "encoding/binary"

// isBigEndian reports this machine's byte order.
//
// Part of the native architecture identity `AUT/AUCOM 231` asks a bundle to
// carry: `linux/arm64` is a target name, and the byte order and word size are
// what make it a statement about the machine the run happened on rather than
// about the string the compiler was given.
func isBigEndian() bool { return binary.NativeEndian.Uint16([]byte{0x01, 0x00}) == 0x0100 }

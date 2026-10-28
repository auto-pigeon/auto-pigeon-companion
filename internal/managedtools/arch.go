package managedtools

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
)

// checkArchitecture refuses a program built for another processor. The
// archive's manifest already names its platform; this reads the program's own
// header, because "never run x86 bytes as ARM" should not rest on a label.
func checkArchitecture(path, target string) error {
	_, arch, _ := strings.Cut(target, "-")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4096)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return refuse("%s cannot be read: %v", path, err)
	}
	header = header[:n]
	found := headerArchitectures(header)
	if len(found) == 0 {
		return refuse("%s is not a program this computer can run (no executable header it recognises)", path)
	}
	for _, candidate := range found {
		if candidate == arch {
			return nil
		}
	}
	return refuse("%s is built for %s and this computer is %s", path, strings.Join(found, ", "), arch)
}

// headerArchitectures names the processors an ELF, PE or Mach-O header (or a
// universal Mach-O) is for, as Go names them.
func headerArchitectures(h []byte) []string {
	switch {
	case len(h) >= 20 && string(h[:4]) == "\x7fELF":
		order := binary.ByteOrder(binary.LittleEndian)
		if h[5] == 2 {
			order = binary.BigEndian
		}
		return machineName(map[uint16]string{62: "amd64", 183: "arm64", 3: "386", 40: "arm"}, order.Uint16(h[18:20]))
	case len(h) >= 64 && h[0] == 'M' && h[1] == 'Z':
		offset := int(binary.LittleEndian.Uint32(h[0x3c:0x40]))
		if offset+6 > len(h) || string(h[offset:offset+4]) != "PE\x00\x00" {
			return nil
		}
		return machineName(map[uint16]string{0x8664: "amd64", 0xaa64: "arm64", 0x14c: "386"}, binary.LittleEndian.Uint16(h[offset+4:offset+6]))
	case len(h) >= 8 && (binary.LittleEndian.Uint32(h[:4]) == 0xfeedfacf || binary.LittleEndian.Uint32(h[:4]) == 0xfeedface):
		return machoName(binary.LittleEndian.Uint32(h[4:8]))
	case len(h) >= 8 && binary.BigEndian.Uint32(h[:4]) == 0xcafebabe:
		count := int(binary.BigEndian.Uint32(h[4:8]))
		var out []string
		for i := 0; i < count && 8+i*20+4 <= len(h); i++ {
			out = append(out, machoName(binary.BigEndian.Uint32(h[8+i*20:8+i*20+4]))...)
		}
		return out
	}
	return nil
}

func machineName(names map[uint16]string, machine uint16) []string {
	if name, ok := names[machine]; ok {
		return []string{name}
	}
	return []string{"an unknown processor"}
}

func machoName(cpu uint32) []string {
	switch cpu {
	case 0x01000007:
		return []string{"amd64"}
	case 0x0100000c:
		return []string{"arm64"}
	}
	return []string{"an unknown processor"}
}

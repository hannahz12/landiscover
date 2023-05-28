package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"strings"

	"github.com/google/gopacket/macs"
)

func defaultInterfaceName() (string, error) {
	intfs, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, in := range intfs {
		// must not be loopback
		if (in.Flags & net.FlagLoopback) != 0 {
			continue
		}

		// must be broadcast capable
		if (in.Flags & net.FlagBroadcast) == 0 {
			continue
		}

		addrs, err := in.Addrs()
		if err != nil {
			continue
		}

		// must have a valid ipv4
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil {
					return in.Name, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no interfaces found")
}

func macVendor(mac net.HardwareAddr) string {
	var pref [3]byte
	copy(pref[:], mac[:3])
	if v, ok := macs.ValidMACPrefixMap[pref]; ok {
		return v
	}
	return "unknown"
}

func copyMac(in net.HardwareAddr) net.HardwareAddr {
	ret := net.HardwareAddr(make([]byte, 6))
	copy(ret, in)
	return ret
}

func copyIP(in net.IP) net.IP {
	ret := net.IP(make([]byte, 4))
	copy(ret, in)
	return ret
}

func randUint16() uint16 {
	var b [2]byte
	rand.Read(b[:])
	return uint16(b[0])<<8 | uint16(b[1])
}

func randUint32() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func randInt63() int64 {
	var b [8]byte
	rand.Read(b[:])
	return int64(uint64(b[0]&0b01111111)<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7]))
}

// https://cs.opensource.google/go/go/+/refs/tags/go1.20.4:src/math/rand/rand.go;l=119
func randInt63n(n int64) int64 {
	if n <= 0 {
		panic("invalid argument to Int63n")
	}
	if n&(n-1) == 0 { // n is power of two, can mask
		return randInt63() & (n - 1)
	}
	max := int64((1 << 63) - 1 - (1<<63)%uint64(n))
	v := randInt63()
	for v > max {
		v = randInt63()
	}
	return v % n
}

// https://cs.opensource.google/go/go/+/refs/tags/go1.20.4:src/math/rand/rand.go;l=160
func randInt31n(n int32) int32 {
	v := randUint32()
	prod := uint64(v) * uint64(n)
	low := uint32(prod)
	if low < uint32(n) {
		thresh := uint32(-n) % uint32(n)
		for low < thresh {
			v = randUint32()
			prod = uint64(v) * uint64(n)
			low = uint32(prod)
		}
	}
	return int32(prod >> 32)
}

// https://cs.opensource.google/go/go/+/refs/tags/go1.20.4:src/math/rand/rand.go;l=246
func randShuffle(n int, swap func(i, j int)) {
	i := n - 1
	for ; i > 1<<31-1-1; i-- {
		j := int(randInt63n(int64(i + 1)))
		swap(i, j)
	}
	for ; i > 0; i-- {
		j := int(randInt31n(int32(i + 1)))
		swap(i, j)
	}
}

func randAvailableIPs(ownIP net.IP) []net.IP {
	var entries []net.IP
	for i := byte(1); i <= 254; i++ {
		eip := make([]byte, 4)
		copy(eip, ownIP)
		eip[3] = i
		if bytes.Equal(eip, ownIP) { // skip own ip
			continue
		}
		entries = append(entries, eip)
	}

	randShuffle(len(entries), func(i, j int) {
		entries[i], entries[j] = entries[j], entries[i]
	})

	return entries
}

// <size>part<size>part
func dnsQueryDecode(data []byte, start int) (string, int) {
	var read []byte
	toread := uint8(0)
	pos := start

	for ; true; pos++ {
		if pos >= len(data) { // decoding terminated before null character
			return "", -1
		}
		if data[pos] == 0x00 {
			if toread > 0 { // decoding terminated before part parsing
				return "", -1
			}
			break // query correctly decoded
		}

		if toread == 0 { // we need a size or pointer
			if len(read) > 0 { // add separator
				read = append(read, '.')
			}

			if (data[pos] & 0xC0) == 0xC0 { // pointer
				ptr := int(binary.BigEndian.Uint16(data[pos:pos+2]) & 0x3FFF)
				pos++ // skip next byte

				substr, subread := dnsQueryDecode(data, ptr)
				if subread <= 0 {
					return "", -1
				}
				read = append(read, []byte(substr)...)
				break // query correctly decoded
			}

			// size
			toread = data[pos]
		} else { // byte inside part
			read = append(read, data[pos])
			toread--
		}
	}
	return string(read), (pos + 1 - start)
}

func dnsQueryEncode(in string) []byte {
	tmp := strings.Split(in, ".")

	l := 0
	for _, part := range tmp {
		bpart := []byte(part)
		l++
		l += len(bpart)
	}
	l++

	ret := make([]byte, l)
	i := 0

	for _, part := range tmp {
		bpart := []byte(part)
		ret[i] = uint8(len(bpart))
		i++
		copy(ret[i:], bpart)
		i += len(bpart)
	}

	ret[i] = uint8(0)

	return ret
}

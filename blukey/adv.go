package blukey

import (
	"encoding/binary"
)

type Adv interface {
	DeviceId() uint32
	AuthKey() uint32
	CanTransact() bool
	SupportsMaintenance() bool
	NeedsMaintenance() bool
}

type AdvV1Flags byte

const (
	AdvV1none            AdvV1Flags = 8
	AdvV1clock           AdvV1Flags = 9
	AdvV1inactivity      AdvV1Flags = 10
	AdvV1cashlessPending AdvV1Flags = 11
	AdvV1cashPending     AdvV1Flags = 12
	AdvV1connectReq      AdvV1Flags = 13
)

type AdvV1Status byte

const (
	AdvV1ready    AdvV1Status = 0
	AdvV1busy     AdvV1Status = 1
	AdvV1disabled AdvV1Status = 2
	AdvV1offline  AdvV1Status = 0xff
)

type AdvV1 struct {
	Id     uint32
	Key    uint32
	Flags  AdvV1Flags
	Status AdvV1Status
}

func (v1 *AdvV1) AuthKey() uint32 {
	return v1.Key
}

func (v1 *AdvV1) CanTransact() bool {
	return v1.Status == AdvV1ready
}

func (v1 *AdvV1) DeviceId() uint32 {
	return v1.Id
}

func (v1 *AdvV1) NeedsMaintenance() bool {
	return v1.Flags != AdvV1none
}

func (v1 *AdvV1) SupportsMaintenance() bool {
	return v1.Flags != 0
}

var v1Name = []byte{0x09, 'P', 'a', 'y', 'R', 'a', 'n', 'g', 'e'}
var v1BRSP = []byte{0x07, 0x79, 0x60, 0x22, 0xa0, 0xbe, 0xaf, 0xc0, 0xbd, 0xde, 0x48, 0x79, 0x62, 0xf1, 0x84, 0x2b, 0xda}

func parseBlukeyV1Adv(raw []byte) *AdvV1 {
	var brsp, name bool
	var msd []byte

	cmp := func(a, b []byte) bool {
		if len(a) != len(b) {
			return false
		}

		for i, v := range a {
			if v != b[i] {
				return false
			}
		}

		return true
	}

	for len(raw) > 1 {
		chunkLen := int(raw[0])
		if chunkLen == 0 || chunkLen+1 > len(raw) {
			break
		}
		chunk := raw[1 : chunkLen+1]
		raw = raw[chunkLen+1:]

		if cmp(chunk, v1Name) {
			name = true
		} else if cmp(chunk, v1BRSP) {
			brsp = true
		} else if chunkLen == 16 && chunk[0] == 0xff && chunk[1] == 0x85 && chunk[2] == 0x00 && chunk[3] == 0xff && chunk[8] == 0x01 && chunk[15] == 0x01 {
			msd = chunk[4:]
		}
	}

	if name && brsp && msd != nil {
		return &AdvV1{
			Id:     binary.LittleEndian.Uint32(msd[0:4]),
			Key:    binary.LittleEndian.Uint32(msd[7:11]),
			Flags:  AdvV1Flags(msd[5]),
			Status: AdvV1Status(msd[6]),
		}
	}

	return nil
}

type AdvV2Flags uint16

const (
	AdvV2canTransact             AdvV2Flags = 0x2000
	AdvV2cashPending             AdvV2Flags = 0x0800
	AdvV2cashlessPending         AdvV2Flags = 0x0400
	AdvV2machAlarmMask           AdvV2Flags = 0x03c0
	AdvV2machAlarmNone           AdvV2Flags = 0x0000
	AdvV2machAlarmInactivity     AdvV2Flags = 0x0040
	AdvV2connAlarmMask           AdvV2Flags = 0x0038
	AdvV2connAlarmNone           AdvV2Flags = 0x0000
	AdvV2connAlarmClockNotSet    AdvV2Flags = 0x0008
	AdvV2connAlarmDebugPending   AdvV2Flags = 0x0010
	AdvV2connAlarmFwUpdateNeeded AdvV2Flags = 0x0018
	AdvV2statusMask              AdvV2Flags = 0x0007
	AdvV2statusReady             AdvV2Flags = 0x0000
	AdvV2statusBusy              AdvV2Flags = 0x0001
	AdvV2statusDisabled          AdvV2Flags = 0x0002
	AdvV2statusReadyMaint        AdvV2Flags = 0x0004
	AdvV2statusOffline           AdvV2Flags = 0x0007
)

type AdvV2 struct {
	Id          uint32
	Key         uint32
	Flags       AdvV2Flags
	FwVersion   uint16
	PartnerData []byte
}

func (v2 *AdvV2) AuthKey() uint32 {
	return v2.Key
}

func (v2 *AdvV2) CanTransact() bool {
	if (v2.Flags & AdvV2statusMask) == AdvV2statusReady {
		return true
	}
	if v2.Flags&AdvV2canTransact != 0 {
		return true
	}

	return false
}

func (v2 *AdvV2) DeviceId() uint32 {
	return v2.Id
}

func (v2 *AdvV2) NeedsMaintenance() bool {
	if v2.Flags&(AdvV2cashPending|AdvV2cashlessPending) != 0 {
		return true
	}
	if v2.Flags&AdvV2connAlarmMask != AdvV2connAlarmNone {
		return true
	}
	return false
}

func (v2 *AdvV2) SupportsMaintenance() bool {
	return true
}

func parseBlukeyV2Adv(raw []byte) *AdvV2 {
	var name bool
	var msd1, msd2 []byte

	for len(raw) > 1 {
		chunkLen := int(raw[0])
		if chunkLen == 0 || chunkLen+1 > len(raw) {
			break
		}
		chunk := raw[1 : chunkLen+1]
		raw = raw[chunkLen+1:]

		if chunkLen == 3 && chunk[0] == 0x09 && chunk[1] == 'P' && chunk[2] == 'R' {
			name = true
		} else if chunkLen == 17 && chunk[0] == 0xff && chunk[1] == 0xc9 && chunk[2] == 0x02 && chunk[3] == 0x00 {
			msd1 = chunk[4:]
		} else if chunkLen > 5 && chunk[0] == 0xff && chunk[1] == 0xc9 && chunk[2] == 0x02 && chunk[3] == 0x01 {
			msd2 = chunk[4:]
		}
	}

	if name && msd1 != nil {
		a := &AdvV2{
			Id:        binary.LittleEndian.Uint32(msd1[0:4]),
			Key:       binary.LittleEndian.Uint32(msd1[4:8]),
			Flags:     AdvV2Flags(binary.LittleEndian.Uint16(msd1[8:10])),
			FwVersion: binary.LittleEndian.Uint16(msd1[10:12]),
		}

		if msd2 != nil {
			a.PartnerData = make([]byte, len(msd2))
			copy(a.PartnerData, msd2)
		}

		return a
	}

	return nil
}

func ParseAdData(raw []byte) Adv {
	if v1 := parseBlukeyV1Adv(raw); v1 != nil {
		return v1
	}

	if v2 := parseBlukeyV2Adv(raw); v2 != nil {
		return v2
	}

	return nil
}

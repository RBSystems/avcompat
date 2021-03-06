package avcompat

import "errors"
import "io"
import "bufio"

// ISC Errors
var (
	ErrIndexRange    = errors.New("Transition index exceeds encoding range")
	ErrSerialLength  = errors.New("Serial transition length cannot exceed 252 bytes")
	ErrDecodeLength  = errors.New("Cannot decode due to short buffer")
	ErrDecodeIllegal = errors.New("Cannot decode due to invalid bitstream")
)

type ISCDigitalTransition struct {
	Index uint
	Value bool
}

type ISCAnalogTransition struct {
	Index uint
	Value uint16
}

type ISCSerialTransition struct {
	Index uint
	Value []byte
}

type ISCClearOperation struct{}
type ISCRefreshOperation struct{}

type ISCDecoder struct {
	r   *bufio.Reader
	err error
}

func (t *ISCDigitalTransition) MarshalBinary() ([]byte, error) {
	var buf [2]byte
	if t.Index > 4095 {
		return nil, ErrIndexRange
	}
	buf[0] = byte(0x80) | byte(0x1f&(t.Index>>7))
	if !t.Value {
		buf[0] |= 0x20 // contains the complement of the value
	}
	buf[1] = byte(0x7f & t.Index)
	return buf[:], nil
}

func (t *ISCDigitalTransition) UnmarshalBinary(buf []byte) error {
	if len(buf) < 2 {
		return ErrDecodeLength
	}
	if (buf[0]&byte(0xC0) != byte(0x80)) || (buf[1]&byte(0x80) != byte(0x00)) {
		return ErrDecodeIllegal
	}

	t.Index = uint(buf[1]) | uint(0x1f&buf[0])<<7
	t.Value = (buf[0]&byte(0x20) == byte(0x00))
	return nil
}

func (t *ISCAnalogTransition) MarshalBinary() ([]byte, error) {
	var buf [4]byte
	if t.Index > 1023 {
		return nil, ErrIndexRange
	}
	buf[0] = byte(0xc0) | byte((t.Value>>14)<<4) | byte(t.Index>>7)
	buf[1] = byte(0x7f & t.Index)
	buf[2] = byte(0x7f & (t.Value >> 7))
	buf[3] = byte(0x7f & t.Value)
	return buf[0:4], nil
}

func (t *ISCAnalogTransition) UnmarshalBinary(buf []byte) error {
	if len(buf) < 4 {
		return ErrDecodeLength
	}
	if (buf[0]&byte(0xC8) != byte(0xC0)) ||
		(buf[1]&byte(0x80) != byte(0x00)) ||
		(buf[2]&byte(0x80) != byte(0x00)) ||
		(buf[3]&byte(0x80) != byte(0x00)) {
		return ErrDecodeIllegal
	}

	t.Index = uint(buf[1]) | uint(0x07&buf[0])<<7
	t.Value = uint16((0x30&buf[0])>>4)<<14 | uint16(buf[2])<<7 | uint16(buf[3])
	return nil
}

func (t *ISCSerialTransition) MarshalBinary() ([]byte, error) {
	if t.Index > 1023 {
		return nil, ErrIndexRange
	}
	if len(t.Value) > 252 {
		return nil, ErrSerialLength
	}
	buf := make([]byte, (2*len(t.Value))+3)
	buf[0] = byte(0xc8) | byte(t.Index>>7)
	buf[1] = byte(0x7f & t.Index)
	p := 2
	for j := range t.Value {
		if t.Value[j] == byte(0xFE) {
			buf[p] = 0xFE
			buf[p+1] = 0x00
			p += 2
		} else if t.Value[j] == byte(0xFF) {
			buf[p] = 0xFE
			buf[p+1] = 0x01
			p += 2
		} else {
			buf[p] = t.Value[j]
			p++
		}
	}
	buf[p] = 0xff
	return buf[0 : p+1], nil
}

func (t *ISCSerialTransition) UnmarshalBinary(buf []byte) error {
	if len(buf) < 3 {
		return ErrDecodeLength
	}
	if buf[len(buf)-1] != 0xff {
		// this has three sane causes:
		// 1: the buffer we have is incomplete, and more data will come (error = ErrDecodeLength)
		// 2: the buffer we have contains more than one packet (error = nil)
		// 3: the buffer contains invalid data (error = ErrDecodeIllegal)
		//
		// we will assume that UnmarshalBinary will always be called with a perfectly
		// framed packet, which renders 1 & 2 impossible.
		return ErrDecodeIllegal
	}

	if (buf[0]&byte(0xF8) != byte(0xC8)) || (buf[1]&byte(0x80) != byte(0x00)) {
		return ErrDecodeIllegal
	}

	t.Index = uint(buf[1]) | uint(0x07&buf[0])<<7
	// this allocation size "just works" since most serial decodes will have few or zero escapes
	// but in the case of a pathological serial transition of all 0xFF values, the destination
	// buffer will still have len < cap. the following appends should not need to re-allocate.
	t.Value = make([]byte, 0, len(buf)-3)
	for j := 2; j < len(buf); j++ {
		switch buf[j] {
		case (byte(0xFE)):
			// we ignore the possibility of a read past the end of the buffer here, because
			// earlier code guarantees that the last byte in the buffer is \xFF.
			switch buf[j+1] {
			case (byte(0x00)):
				t.Value = append(t.Value, byte(0xFE))
				j++
			case (byte(0x01)):
				t.Value = append(t.Value, byte(0xFF))
				j++
			default:
				// invalid escape sequence
				return ErrDecodeIllegal
			}
		case (byte(0xFF)):
			if j != len(buf)-1 {
				// we received an end-of-packet byte, but there's more bytes remaining
				return ErrDecodeIllegal
			}
			return nil
		default:
			t.Value = append(t.Value, buf[j])
		}
	}
	// this return statement shouldn't be reachable with any valid encoding
	return ErrDecodeIllegal
}

func (o *ISCClearOperation) MarshalBinary() ([]byte, error) {
	return []byte{0xFC}, nil
}

func (o *ISCClearOperation) UnmarshalBinary(buf []byte) error {
	if len(buf) < 1 {
		return ErrDecodeLength
	}
	if buf[0] != 0xFC {
		return ErrDecodeIllegal
	}
	return nil
}

func (o *ISCRefreshOperation) MarshalBinary() ([]byte, error) {
	return []byte{0xFD}, nil
}

func (o *ISCRefreshOperation) UnmarshalBinary(buf []byte) error {
	if len(buf) < 1 {
		return ErrDecodeLength
	}
	if buf[0] != 0xFD {
		return ErrDecodeIllegal
	}
	return nil
}

func NewISCDecoder(r io.Reader) *ISCDecoder {
	return &ISCDecoder{r: bufio.NewReader(r)}
}

func (d *ISCDecoder) Decode() (v interface{}, err error) {
	if d.r == nil {
		return nil, errors.New("ISCDecoder in invalid state")
	}
	defer func() { d.err = err }()

	buf := make([]byte, 256)

	if d.err != nil {
		err = d.err
		return
	}

	p, err := d.r.Peek(1)
	if err != nil {
		return
	}
	// Clear Operation
	if p[0] == byte(0xFC) {
		_, err = io.ReadFull(d.r, buf[0:1])
		if err != nil {
			return
		}

		var res ISCClearOperation
		err = res.UnmarshalBinary(buf[0:1])
		v = res
		return
	}
	// Refresh Operation
	if p[0] == byte(0xFD) {
		_, err = io.ReadFull(d.r, buf[0:1])
		if err != nil {
			return
		}

		var res ISCRefreshOperation
		err = res.UnmarshalBinary(buf[0:1])
		v = res
		return
	}
	// Digital Transition
	if p[0]&byte(0xC0) == byte(0x80) {
		_, err = io.ReadFull(d.r, buf[0:2])
		if err != nil {
			return
		}

		var res ISCDigitalTransition
		err = res.UnmarshalBinary(buf[0:2])
		v = res
		return
	}
	// Analog Transition
	if p[0]&byte(0xC8) == byte(0xC0) {
		_, err = io.ReadFull(d.r, buf[0:4])
		if err != nil {
			return
		}

		var res ISCAnalogTransition
		err = res.UnmarshalBinary(buf)
		v = res
		return
	}
	// Serial Transition
	if p[0]&byte(0xF8) == byte(0xC8) {
		buf, err = d.r.ReadBytes(0xFF)
		if err != nil {
			return
		}

		var res ISCSerialTransition
		err = res.UnmarshalBinary(buf)
		v = res
		return
	}

	return
}

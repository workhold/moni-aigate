package server

import (
	"bytes"
	"encoding/binary"
	"errors"
)

var errInvalidSpeechWAV = errors.New("invalid_speech_wav")

func validateSpeechWAV(data []byte) error {
	if len(data) < 44 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return errInvalidSpeechWAV
	}
	if int64(binary.LittleEndian.Uint32(data[4:8]))+8 != int64(len(data)) {
		return errInvalidSpeechWAV
	}
	const (
		pcmFormat  = uint16(1)
		mono       = uint16(1)
		sampleRate = uint32(8000)
		byteRate   = uint32(16000)
		blockAlign = uint16(2)
		bits       = uint16(16)
	)
	var formatSeen, dataSeen bool
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return errInvalidSpeechWAV
		}
		chunkSize := int64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payloadStart := offset + 8
		payloadEnd := int64(payloadStart) + chunkSize
		if payloadEnd > int64(len(data)) {
			return errInvalidSpeechWAV
		}
		paddedEnd := payloadEnd
		if chunkSize%2 != 0 {
			paddedEnd++
		}
		if paddedEnd > int64(len(data)) {
			return errInvalidSpeechWAV
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if formatSeen || chunkSize != 16 {
				return errInvalidSpeechWAV
			}
			formatSeen = true
			format := data[payloadStart:payloadEnd]
			if binary.LittleEndian.Uint16(format[0:2]) != pcmFormat || binary.LittleEndian.Uint16(format[2:4]) != mono || binary.LittleEndian.Uint32(format[4:8]) != sampleRate || binary.LittleEndian.Uint32(format[8:12]) != byteRate || binary.LittleEndian.Uint16(format[12:14]) != blockAlign || binary.LittleEndian.Uint16(format[14:16]) != bits {
				return errInvalidSpeechWAV
			}
		case "data":
			if dataSeen || chunkSize == 0 || chunkSize%int64(blockAlign) != 0 {
				return errInvalidSpeechWAV
			}
			dataSeen = true
		}
		offset = int(paddedEnd)
	}
	if !formatSeen || !dataSeen {
		return errInvalidSpeechWAV
	}
	return nil
}

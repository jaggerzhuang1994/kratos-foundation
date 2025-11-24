package utils

import (
	"bytes"
	"encoding/gob"
)

type CustomEncode = func(*gob.Encoder) error
type CustomDecode = func(*gob.Decoder) error

func NilPtrGobEncodeHelper[T any](ptrV *T) CustomEncode {
	return func(e *gob.Encoder) (err error) {
		err = e.Encode(ptrV != nil)
		if err != nil {
			return err
		}
		if ptrV != nil {
			err = e.Encode(ptrV)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func NilPtrSliceGobEncodeHelper[T any](s []*T) CustomEncode {
	return func(e *gob.Encoder) (err error) {
		err = e.Encode(len(s))
		if err != nil {
			return err
		}
		for i := range s {
			err = NilPtrGobEncodeHelper(s[i])(e)
			if err != nil {
				return err
			}
		}

		return nil
	}
}

func NilPtrGobDecodeHelper[T any](ppv **T) CustomDecode {
	return func(d *gob.Decoder) (err error) {
		var isNotNil bool
		err = d.Decode(&isNotNil)
		if err != nil {
			return err
		}
		if isNotNil {
			err = d.Decode(ppv)
			if err != nil {
				return err
			}
		} else {
			*ppv = nil
		}
		return nil
	}
}

func NilPtrSliceGobDecodeHelper[T any](ps *[]*T) CustomDecode {
	return func(d *gob.Decoder) (err error) {
		var l int
		err = d.Decode(&l)
		if err != nil {
			return err
		}
		*ps = make([]*T, l)
		for i := range *ps {
			err = NilPtrGobDecodeHelper(&(*ps)[i])(d)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func GobEncodeHelper(values ...interface{}) ([]byte, error) {
	b := &bytes.Buffer{}
	e := gob.NewEncoder(b)

	var err error
	for i := range values {
		switch values[i].(type) {
		case CustomEncode:
			err = values[i].(CustomEncode)(e)
		default:
			err = e.Encode(values[i])
		}
		if err != nil {
			return nil, err
		}
	}

	return b.Bytes(), nil
}
func GobDecodeHelper(buf []byte, ptrs ...interface{}) error {
	b := bytes.NewBuffer(buf)
	d := gob.NewDecoder(b)

	var err error
	for i := range ptrs {
		switch ptrs[i].(type) {
		case CustomDecode:
			err = ptrs[i].(CustomDecode)(d)
		default:
			err = d.Decode(ptrs[i])
		}
		if err != nil {
			return err
		}
	}

	return nil
}

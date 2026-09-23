package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Signed/operator JSON must have one unambiguous interpretation. The standard
// decoder's last-duplicate-wins behavior is unsuitable at this trust boundary.
func decodeStrict(reader io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("JSON too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("JSON too deep")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'):
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return errors.New("duplicate JSON key")
				}
				keys[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return errors.New("JSON object invalid")
			}
		case json.Delim('['):
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return errors.New("JSON array invalid")
			}
		}
		return nil
	}
	if err = value(0); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

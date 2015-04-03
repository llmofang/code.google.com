// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package collate contains types for comparing and sorting Unicode strings
// according to a given collation order.  Package locale provides a high-level
// interface to collation. Users should typically use that package instead.
package collate

import (
	"bytes"
	"strings"

	"code.google.com/p/go.text/collate/colltab"
	"code.google.com/p/go.text/language"
	"code.google.com/p/go.text/unicode/norm"
)

// AlternateHandling identifies the various ways in which variables are handled.
// A rune with a primary weight lower than the variable top is considered a
// variable.
// See http://www.unicode.org/reports/tr10/#Variable_Weighting for details.
type AlternateHandling int

const (
	// AltNonIgnorable turns off special handling of variables.
	AltNonIgnorable AlternateHandling = iota

	// AltBlanked sets variables and all subsequent primary ignorables to be
	// ignorable at all levels. This is identical to removing all variables
	// and subsequent primary ignorables from the input.
	AltBlanked

	// AltShifted sets variables to be ignorable for levels one through three and
	// adds a fourth level based on the values of the ignored levels.
	AltShifted

	// AltShiftTrimmed is a slight variant of AltShifted that is used to
	// emulate POSIX.
	AltShiftTrimmed
)

// Collator provides functionality for comparing strings for a given
// collation order.
type Collator struct {
	// TODO: hide most of these options. Low-level options are set through the locale
	// identifier (as defined by LDML) while high-level options are set through SetOptions.
	// Using high-level options allows us to be more flexible (such as not ignoring
	// Thai vowels for IgnoreDiacriticals) and more user-friendly (such as allowing
	// diacritical marks to be ignored but not case without having to fiddle with levels).

	// Strength sets the maximum level to use in comparison.
	Strength colltab.Level

	// Alternate specifies an alternative handling of variables.
	Alternate AlternateHandling

	// Backwards specifies the order of sorting at the secondary level.
	// This option exists predominantly to support reverse sorting of accents in French.
	Backwards bool

	// TODO: implement:
	// With HiraganaQuaternary enabled, Hiragana codepoints will get lower values
	// than all the other non-variable code points. Strength must be greater or
	// equal to Quaternary for this to take effect.
	HiraganaQuaternary bool

	// If CaseLevel is true, a level consisting only of case characteristics will
	// be inserted in front of the tertiary level.  To ignore accents but take
	// cases into account, set Strength to Primary and CaseLevel to true.
	CaseLevel bool

	// If Numeric is true, any sequence of decimal digits (category is Nd) is sorted
	// at a primary level with its numeric value.  For example, "A-21" < "A-123".
	Numeric bool

	// The largest primary value that is considered to be variable.
	variableTop uint32

	f norm.Form

	t colltab.Weigher

	sorter sorter

	_iter [2]iter
}

// An Option is used to change the behavior of Collator.  They override the
// settings passed through the locale identifier.
type Option int

const (
	Numeric          Option = 1 << iota // Sort numbers numerically ("2" < "12").
	IgnoreCase                          // Case-insensitive search.
	IgnoreDiacritics                    // Ignore diacritical marks. ("o" == "ö").
	IgnoreWidth                         // Ignore full versus normal width.
	UpperFirst                          // Sort upper case before lower case.
	LowerFirst                          // Sort lower case before upper case.
	Force                               // Force ordering if strings are equivalent but not equal.

	Loose = IgnoreDiacritics | IgnoreWidth | IgnoreCase
)

// SetOptions accepts a Options or-ed together.  All previous calls to SetOptions are ignored.
func (c *Collator) SetOptions(o Option) {
	// TODO: implement
}

func (c *Collator) iter(i int) *iter {
	// TODO: evaluate performance for making the second iterator optional.
	return &c._iter[i]
}

// Supported returns the list of languages for which collating differs from its parent.
func Supported() []language.Tag {
	ids := strings.Split(availableLocales, ",")
	tags := make([]language.Tag, len(ids))
	for i, s := range ids {
		tags[i] = language.Make(s)
	}
	return tags
}

var matcher = language.NewMatcher(Supported())

// New returns a new Collator initialized for the given locale.
func New(t language.Tag) *Collator {
	_, index, _ := matcher.Match(t)
	return NewFromTable(colltab.Init(locales[index]))
}

func NewFromTable(t colltab.Weigher) *Collator {
	c := &Collator{
		Strength: colltab.Tertiary,
		f:        norm.NFD,
		t:        t,
	}
	c._iter[0].init(c)
	c._iter[1].init(c)
	c.variableTop = t.Top()
	return c
}

// Buffer holds keys generated by Key and KeyString.
type Buffer struct {
	buf [4096]byte
	key []byte
}

func (b *Buffer) init() {
	if b.key == nil {
		b.key = b.buf[:0]
	}
}

// Reset clears the buffer from previous results generated by Key and KeyString.
func (b *Buffer) Reset() {
	b.key = b.key[:0]
}

// Compare returns an integer comparing the two byte slices.
// The result will be 0 if a==b, -1 if a < b, and +1 if a > b.
func (c *Collator) Compare(a, b []byte) int {
	// TODO: skip identical prefixes once we have a fast way to detect if a rune is
	// part of a contraction. This would lead to roughly a 10% speedup for the colcmp regtest.
	c.iter(0).setInput(a)
	c.iter(1).setInput(b)
	if res := c.compare(); res != 0 {
		return res
	}
	if colltab.Identity == c.Strength {
		return bytes.Compare(a, b)
	}
	return 0
}

// CompareString returns an integer comparing the two strings.
// The result will be 0 if a==b, -1 if a < b, and +1 if a > b.
func (c *Collator) CompareString(a, b string) int {
	// TODO: skip identical prefixes once we have a fast way to detect if a rune is
	// part of a contraction. This would lead to roughly a 10% speedup for the colcmp regtest.
	c.iter(0).setInputString(a)
	c.iter(1).setInputString(b)
	if res := c.compare(); res != 0 {
		return res
	}
	if colltab.Identity == c.Strength {
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
	}
	return 0
}

func compareLevel(f func(i *iter) int, a, b *iter) int {
	a.pce = 0
	b.pce = 0
	for {
		va := f(a)
		vb := f(b)
		if va != vb {
			if va < vb {
				return -1
			}
			return 1
		} else if va == 0 {
			break
		}
	}
	return 0
}

func (c *Collator) compare() int {
	ia, ib := c.iter(0), c.iter(1)
	// Process primary level
	if c.Alternate != AltShifted {
		// TODO: implement script reordering
		// TODO: special hiragana handling
		if res := compareLevel((*iter).nextPrimary, ia, ib); res != 0 {
			return res
		}
	} else {
		// TODO: handle shifted
	}
	if colltab.Secondary <= c.Strength {
		f := (*iter).nextSecondary
		if c.Backwards {
			f = (*iter).prevSecondary
		}
		if res := compareLevel(f, ia, ib); res != 0 {
			return res
		}
	}
	// TODO: special case handling (Danish?)
	if colltab.Tertiary <= c.Strength || c.CaseLevel {
		if res := compareLevel((*iter).nextTertiary, ia, ib); res != 0 {
			return res
		}
		// TODO: Not needed for the default value of AltNonIgnorable?
		if colltab.Quaternary <= c.Strength {
			if res := compareLevel((*iter).nextQuaternary, ia, ib); res != 0 {
				return res
			}
		}
	}
	return 0
}

// Key returns the collation key for str.
// Passing the buffer buf may avoid memory allocations.
// The returned slice will point to an allocation in Buffer and will remain
// valid until the next call to buf.Reset().
func (c *Collator) Key(buf *Buffer, str []byte) []byte {
	// See http://www.unicode.org/reports/tr10/#Main_Algorithm for more details.
	buf.init()
	return c.key(buf, c.getColElems(str))
}

// KeyFromString returns the collation key for str.
// Passing the buffer buf may avoid memory allocations.
// The returned slice will point to an allocation in Buffer and will retain
// valid until the next call to buf.ResetKeys().
func (c *Collator) KeyFromString(buf *Buffer, str string) []byte {
	// See http://www.unicode.org/reports/tr10/#Main_Algorithm for more details.
	buf.init()
	return c.key(buf, c.getColElemsString(str))
}

func (c *Collator) key(buf *Buffer, w []colltab.Elem) []byte {
	processWeights(c.Alternate, c.t.Top(), w)
	kn := len(buf.key)
	c.keyFromElems(buf, w)
	return buf.key[kn:]
}

func (c *Collator) getColElems(str []byte) []colltab.Elem {
	i := c.iter(0)
	i.setInput(str)
	for i.next() {
	}
	return i.ce
}

func (c *Collator) getColElemsString(str string) []colltab.Elem {
	i := c.iter(0)
	i.setInputString(str)
	for i.next() {
	}
	return i.ce
}

type iter struct {
	bytes []byte
	str   string

	wa  [512]colltab.Elem
	ce  []colltab.Elem
	pce int
	nce int // nce <= len(nce)

	prevCCC  uint8
	pStarter int

	t colltab.Weigher
}

func (i *iter) init(c *Collator) {
	i.t = c.t
	i.ce = i.wa[:0]
}

func (i *iter) reset() {
	i.ce = i.ce[:0]
	i.nce = 0
	i.prevCCC = 0
	i.pStarter = 0
}

func (i *iter) setInput(s []byte) *iter {
	i.bytes = s
	i.str = ""
	i.reset()
	return i
}

func (i *iter) setInputString(s string) *iter {
	i.str = s
	i.bytes = nil
	i.reset()
	return i
}

func (i *iter) done() bool {
	return len(i.str) == 0 && len(i.bytes) == 0
}

func (i *iter) tail(n int) {
	if i.bytes == nil {
		i.str = i.str[n:]
	} else {
		i.bytes = i.bytes[n:]
	}
}

func (i *iter) appendNext() int {
	var sz int
	if i.bytes == nil {
		i.ce, sz = i.t.AppendNextString(i.ce, i.str)
	} else {
		i.ce, sz = i.t.AppendNext(i.ce, i.bytes)
	}
	return sz
}

// next appends Elems to the internal array until it adds an element with CCC=0.
// In the majority of cases, a Elem with a primary value > 0 will have
// a CCC of 0. The CCC values of colation elements are also used to detect if the
// input string was not normalized and to adjust the result accordingly.
func (i *iter) next() bool {
	for !i.done() {
		p0 := len(i.ce)
		sz := i.appendNext()
		i.tail(sz)
		last := len(i.ce) - 1
		if ccc := i.ce[last].CCC(); ccc == 0 {
			i.nce = len(i.ce)
			i.pStarter = last
			i.prevCCC = 0
			return true
		} else if p0 < last && i.ce[p0].CCC() == 0 {
			// set i.nce to only cover part of i.ce for which ccc == 0 and
			// use rest the next call to next.
			for p0++; p0 < last && i.ce[p0].CCC() == 0; p0++ {
			}
			i.nce = p0
			i.pStarter = p0 - 1
			i.prevCCC = ccc
			return true
		} else if ccc < i.prevCCC {
			i.doNorm(p0, ccc) // should be rare for most common cases
		} else {
			i.prevCCC = ccc
		}
	}
	if len(i.ce) != i.nce {
		i.nce = len(i.ce)
		return true
	}
	return false
}

// nextPlain is the same as next, but does not "normalize" the collation
// elements.
// TODO: remove this function. Using this instead of next does not seem
// to improve performance in any significant way. We retain this until
// later for evaluation purposes.
func (i *iter) nextPlain() bool {
	if i.done() {
		return false
	}
	sz := i.appendNext()
	i.tail(sz)
	i.nce = len(i.ce)
	return true
}

const maxCombiningCharacters = 30

// doNorm reorders the collation elements in i.ce.
// It assumes that blocks of collation elements added with appendNext
// either start and end with the same CCC or start with CCC == 0.
// This allows for a single insertion point for the entire block.
// The correctness of this assumption is verified in builder.go.
func (i *iter) doNorm(p int, ccc uint8) {
	if p-i.pStarter > maxCombiningCharacters {
		i.prevCCC = i.ce[len(i.ce)-1].CCC()
		i.pStarter = len(i.ce) - 1
		return
	}
	n := len(i.ce)
	k := p
	for p--; p > i.pStarter && ccc < i.ce[p-1].CCC(); p-- {
	}
	i.ce = append(i.ce, i.ce[p:k]...)
	copy(i.ce[p:], i.ce[k:])
	i.ce = i.ce[:n]
}

func (i *iter) nextPrimary() int {
	for {
		for ; i.pce < i.nce; i.pce++ {
			if v := i.ce[i.pce].Primary(); v != 0 {
				i.pce++
				return v
			}
		}
		if !i.next() {
			return 0
		}
	}
	panic("should not reach here")
}

func (i *iter) nextSecondary() int {
	for ; i.pce < len(i.ce); i.pce++ {
		if v := i.ce[i.pce].Secondary(); v != 0 {
			i.pce++
			return v
		}
	}
	return 0
}

func (i *iter) prevSecondary() int {
	for ; i.pce < len(i.ce); i.pce++ {
		if v := i.ce[len(i.ce)-i.pce-1].Secondary(); v != 0 {
			i.pce++
			return v
		}
	}
	return 0
}

func (i *iter) nextTertiary() int {
	for ; i.pce < len(i.ce); i.pce++ {
		if v := i.ce[i.pce].Tertiary(); v != 0 {
			i.pce++
			return int(v)
		}
	}
	return 0
}

func (i *iter) nextQuaternary() int {
	for ; i.pce < len(i.ce); i.pce++ {
		if v := i.ce[i.pce].Quaternary(); v != 0 {
			i.pce++
			return v
		}
	}
	return 0
}

func appendPrimary(key []byte, p int) []byte {
	// Convert to variable length encoding; supports up to 23 bits.
	if p <= 0x7FFF {
		key = append(key, uint8(p>>8), uint8(p))
	} else {
		key = append(key, uint8(p>>16)|0x80, uint8(p>>8), uint8(p))
	}
	return key
}

// keyFromElems converts the weights ws to a compact sequence of bytes.
// The result will be appended to the byte buffer in buf.
func (c *Collator) keyFromElems(buf *Buffer, ws []colltab.Elem) {
	for _, v := range ws {
		if w := v.Primary(); w > 0 {
			buf.key = appendPrimary(buf.key, w)
		}
	}
	if colltab.Secondary <= c.Strength {
		buf.key = append(buf.key, 0, 0)
		// TODO: we can use one 0 if we can guarantee that all non-zero weights are > 0xFF.
		if !c.Backwards {
			for _, v := range ws {
				if w := v.Secondary(); w > 0 {
					buf.key = append(buf.key, uint8(w>>8), uint8(w))
				}
			}
		} else {
			for i := len(ws) - 1; i >= 0; i-- {
				if w := ws[i].Secondary(); w > 0 {
					buf.key = append(buf.key, uint8(w>>8), uint8(w))
				}
			}
		}
	} else if c.CaseLevel {
		buf.key = append(buf.key, 0, 0)
	}
	if colltab.Tertiary <= c.Strength || c.CaseLevel {
		buf.key = append(buf.key, 0, 0)
		for _, v := range ws {
			if w := v.Tertiary(); w > 0 {
				buf.key = append(buf.key, uint8(w))
			}
		}
		// Derive the quaternary weights from the options and other levels.
		// Note that we represent MaxQuaternary as 0xFF. The first byte of the
		// representation of a primary weight is always smaller than 0xFF,
		// so using this single byte value will compare correctly.
		if colltab.Quaternary <= c.Strength && c.Alternate >= AltShifted {
			if c.Alternate == AltShiftTrimmed {
				lastNonFFFF := len(buf.key)
				buf.key = append(buf.key, 0)
				for _, v := range ws {
					if w := v.Quaternary(); w == colltab.MaxQuaternary {
						buf.key = append(buf.key, 0xFF)
					} else if w > 0 {
						buf.key = appendPrimary(buf.key, w)
						lastNonFFFF = len(buf.key)
					}
				}
				buf.key = buf.key[:lastNonFFFF]
			} else {
				buf.key = append(buf.key, 0)
				for _, v := range ws {
					if w := v.Quaternary(); w == colltab.MaxQuaternary {
						buf.key = append(buf.key, 0xFF)
					} else if w > 0 {
						buf.key = appendPrimary(buf.key, w)
					}
				}
			}
		}
	}
}

func processWeights(vw AlternateHandling, top uint32, wa []colltab.Elem) {
	ignore := false
	vtop := int(top)
	switch vw {
	case AltShifted, AltShiftTrimmed:
		for i := range wa {
			if p := wa[i].Primary(); p <= vtop && p != 0 {
				wa[i] = colltab.MakeQuaternary(p)
				ignore = true
			} else if p == 0 {
				if ignore {
					wa[i] = colltab.Ignore
				}
			} else {
				ignore = false
			}
		}
	case AltBlanked:
		for i := range wa {
			if p := wa[i].Primary(); p <= vtop && (ignore || p != 0) {
				wa[i] = colltab.Ignore
				ignore = true
			} else {
				ignore = false
			}
		}
	}
}

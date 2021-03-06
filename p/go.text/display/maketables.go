// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// Generator for display name tables.

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"

	"code.google.com/p/go.text/cldr"
	"code.google.com/p/go.text/language"
)

var (
	url = flag.String("cldr",
		"http://www.unicode.org/Public/cldr/"+cldr.Version+"/core.zip",
		"URL of CLDR archive.")
	iana = flag.String("iana",
		"http://www.iana.org/assignments/language-subtag-registry",
		"URL of IANA language subtag registry.")
	test = flag.Bool("test", false,
		"test existing tables; can be used to compare web data with package data.")
	localFiles = flag.Bool("local", false,
		"data files have been copied to the current directory; for debugging only.")
	stats = flag.Bool("stats", false, "prints statistics to stderr")

	short = flag.Bool("short", false, `Use "short" alternatives, when available.`)
	draft = flag.String("draft", "contributed",
		`Minimal draft requirements (approved, contributed, provisional, unconfirmed).`)
	pkg = flag.String("package", "display",
		"the name of the package in which the generated file is to be included")

	tags = newTagSet("tags", []language.Tag{},
		"space-separated list of tags to include or empty for all")
	dict = newTagSet("dict", dictTags(),
		"space-separated list or tags for which to include a Dictionary. "+
			`"" means the common list from go.text/language.`)
)

func dictTags() (tag []language.Tag) {
	// TODO: replace with language.Common.Tags() once supported.
	const str = "af am ar ar-001 az bg bn ca cs da de el en en-US en-GB " +
		"es es-ES es-419 et fa fi fil fr fr-CA gu he hi hr hu hy id is it ja " +
		"ka kk km kn ko ky lo lt lv mk ml mn mr ms my ne nl no pa pl pt pt-BR " +
		"pt-PT ro ru si sk sl sq sr sv sw ta te th tr uk ur uz vi zh zh-Hans " +
		"zh-Hant zu"

	for _, s := range strings.Split(str, " ") {
		tag = append(tag, language.MustParse(s))
	}
	return tag
}

func main() {
	flag.Parse()

	// Read the CLDR zip file.
	if *localFiles {
		pwd, _ := os.Getwd()
		*url = "file://" + path.Join(pwd, path.Base(*url))
	}
	t := &http.Transport{}
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	c := &http.Client{Transport: t}
	resp, err := c.Get(*url)
	if err != nil {
		log.Fatalf("HTTP GET: %v", err)
	}
	if resp.StatusCode != 200 {
		log.Fatalf(`bad GET status for "%q": %q`, *url, resp.Status)
	}
	r := resp.Body
	defer r.Close()

	d := &cldr.Decoder{}
	d.SetDirFilter("main", "supplemental")
	d.SetSectionFilter("localeDisplayNames")
	data, err := d.DecodeZip(r)
	if err != nil {
		log.Fatalf("DecodeZip: %v", err)
	}
	b := builder{
		data:  data,
		group: make(map[string]*group),
	}
	b.generate()
}

const tagForm = language.All

// tagSet is used to parse command line flags of tags. It implements the
// flag.Value interface.
type tagSet map[language.Tag]bool

func newTagSet(name string, tags []language.Tag, usage string) tagSet {
	f := tagSet(make(map[language.Tag]bool))
	for _, t := range tags {
		f[t] = true
	}
	flag.Var(f, name, usage)
	return f
}

// String implements the String method of the flag.Value interface.
func (f tagSet) String() string {
	tags := []string{}
	for t := range f {
		tags = append(tags, t.String())
	}
	sort.Strings(tags)
	return strings.Join(tags, " ")
}

// Set implements Set from the flag.Value interface.
func (f tagSet) Set(s string) error {
	if s != "" {
		for _, s := range strings.Split(s, " ") {
			if s != "" {
				tag, err := tagForm.Parse(s)
				if err != nil {
					return err
				}
				f[tag] = true
			}
		}
	}
	return nil
}

func (f tagSet) contains(t language.Tag) bool {
	if len(f) == 0 {
		return true
	}
	return f[t]
}

// builder is used to create all tables with display name information.
type builder struct {
	data *cldr.CLDR

	fromLocs []string

	// destination tags for the current locale.
	toTags     []string
	toTagIndex map[string]int

	// list of supported tags
	supported []language.Tag

	// key-value pairs per group
	group map[string]*group

	// statistics
	sizeIndex int // total size of all indexes of headers
	sizeData  int // total size of all data of headers
	totalSize int
}

type group struct {
	// Maps from a given language to the Namer data for this language.
	lang    map[language.Tag]keyValues
	headers []header

	toTags        []string
	threeStart    int
	fourPlusStart int
}

// set sets the typ to the name for locale loc.
func (g *group) set(t language.Tag, typ, name string) {
	kv := g.lang[t]
	if kv == nil {
		kv = make(keyValues)
		g.lang[t] = kv
	}
	if kv[typ] == "" {
		kv[typ] = name
	}
}

type keyValues map[string]string

type header struct {
	tag   language.Tag
	data  string
	index []uint16
}

var head = `// Generated by running
//		maketables -url=%s
// DO NOT EDIT

package %s

// Version is the version of CLDR used to generate the data in this package.
var Version = %#v

`

var self = language.MustParse("mul")

// generate builds and writes all tables.
func (b *builder) generate() {
	fmt.Printf(head, *url, *pkg, cldr.Version)

	b.filter()
	b.setData("lang", func(g *group, loc language.Tag, ldn *cldr.LocaleDisplayNames) {
		if ldn.Languages != nil {
			for _, v := range ldn.Languages.Language {
				tag := tagForm.MustParse(v.Type)
				if tags.contains(tag) {
					g.set(loc, tag.String(), v.Data())
				}
			}
		}
	})
	b.setData("script", func(g *group, loc language.Tag, ldn *cldr.LocaleDisplayNames) {
		if ldn.Scripts != nil {
			for _, v := range ldn.Scripts.Script {
				g.set(loc, language.MustParseScript(v.Type).String(), v.Data())
			}
		}
	})
	b.setData("region", func(g *group, loc language.Tag, ldn *cldr.LocaleDisplayNames) {
		if ldn.Territories != nil {
			for _, v := range ldn.Territories.Territory {
				g.set(loc, language.MustParseRegion(v.Type).String(), v.Data())
			}
		}
	})

	b.makeSupported()

	n := b.writeParents()

	n += b.writeGroup("lang")
	n += b.writeGroup("script")
	n += b.writeGroup("region")

	b.writeSupported()

	n += b.writeDictionaries()

	b.supported = []language.Tag{self}

	// Compute the names of locales in their own language. Some of these names
	// may be specified in their parent locales. We iterate the maximum depth
	// of the parent three times to match successive parents of tags until a
	// possible match is found.
	for i := 0; i < 4; i++ {
		b.setData("self", func(g *group, tag language.Tag, ldn *cldr.LocaleDisplayNames) {
			parent := tag
			if b, s, r := tag.Raw(); i > 0 && (s != language.Script{} && r == language.Region{}) {
				parent, _ = language.Raw.Compose(b)
			}
			if ldn.Languages != nil {
				for _, v := range ldn.Languages.Language {
					key := tagForm.MustParse(v.Type)
					saved := key
					if key == parent {
						g.set(self, tag.String(), v.Data())
					}
					for k := 0; k < i; k++ {
						key = key.Parent()
					}
					if key == tag {
						g.set(self, saved.String(), v.Data()) // set does not overwrite a value.
					}
				}
			}
		})
	}

	n += b.writeGroup("self")

	fmt.Printf("// TOTAL %d Bytes (%d KB)", n, n/1000)
}

func (b *builder) setData(name string, f func(*group, language.Tag, *cldr.LocaleDisplayNames)) {
	b.sizeIndex = 0
	b.sizeData = 0
	b.toTags = nil
	b.fromLocs = nil
	b.toTagIndex = make(map[string]int)

	g := b.group[name]
	if g == nil {
		g = &group{lang: make(map[language.Tag]keyValues)}
		b.group[name] = g
	}
	for _, loc := range b.data.Locales() {
		// We use RawLDML instead of LDML as we are managing our own inheritance
		// in this implementation.
		ldml := b.data.RawLDML(loc)

		// We do not support the POSIX variant (it is not a supported BCP 47
		// variant). This locale also doesn't happen to contain any data, so
		// we'll skip it by checking for this.
		tag, err := tagForm.Parse(loc)
		if err != nil {
			if ldml.LocaleDisplayNames != nil {
				log.Fatalf("setData: %v", err)
			}
			continue
		}
		if ldml.LocaleDisplayNames != nil && tags.contains(tag) {
			f(g, tag, ldml.LocaleDisplayNames)
		}
	}
}

func (b *builder) filter() {
	filter := func(s *cldr.Slice) {
		if *short {
			s.SelectOnePerGroup("alt", []string{"short", ""})
		} else {
			s.SelectOnePerGroup("alt", []string{"stand-alone", ""})
		}
		d, err := cldr.ParseDraft(*draft)
		if err != nil {
			log.Fatalf("filter: %v", err)
		}
		s.SelectDraft(d)
	}
	for _, loc := range b.data.Locales() {
		if ldn := b.data.RawLDML(loc).LocaleDisplayNames; ldn != nil {
			if ldn.Languages != nil {
				s := cldr.MakeSlice(&ldn.Languages.Language)
				if filter(&s); len(ldn.Languages.Language) == 0 {
					ldn.Languages = nil
				}
			}
			if ldn.Scripts != nil {
				s := cldr.MakeSlice(&ldn.Scripts.Script)
				if filter(&s); len(ldn.Scripts.Script) == 0 {
					ldn.Scripts = nil
				}
			}
			if ldn.Territories != nil {
				s := cldr.MakeSlice(&ldn.Territories.Territory)
				if filter(&s); len(ldn.Territories.Territory) == 0 {
					ldn.Territories = nil
				}
			}
		}
	}
}

// makeSupported creates a list of all supported locales.
func (b *builder) makeSupported() {
	// tags across groups
	for _, g := range b.group {
		for t, _ := range g.lang {
			b.supported = append(b.supported, t)
		}
	}
	b.supported = b.supported[:unique(tagsSorter(b.supported))]

}

type tagsSorter []language.Tag

func (a tagsSorter) Len() int           { return len(a) }
func (a tagsSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a tagsSorter) Less(i, j int) bool { return a[i].String() < a[j].String() }

func (b *builder) writeGroup(name string) int {
	g := b.group[name]

	for _, kv := range g.lang {
		for t, _ := range kv {
			g.toTags = append(g.toTags, t)
		}
	}
	g.toTags = g.toTags[:unique(tagsBySize(g.toTags))]

	// Allocate header per supported value.
	g.headers = make([]header, len(b.supported))
	for i, sup := range b.supported {
		kv, ok := g.lang[sup]
		if !ok {
			g.headers[i].tag = sup
			continue
		}
		data := []byte{}
		index := make([]uint16, len(g.toTags), len(g.toTags)+1)
		for j, t := range g.toTags {
			index[j] = uint16(len(data))
			data = append(data, kv[t]...)
		}
		index = append(index, uint16(len(data)))

		// Trim the tail of the index.
		// TODO: indexes can be reduced in size quite a bit more.
		n := len(index)
		for ; n >= 2 && index[n-2] == index[n-1]; n-- {
		}
		index = index[:n]

		g.headers[i] = header{sup, string(data), index}
	}
	return g.writeTable(name)
}

type tagsBySize []string

func (l tagsBySize) Len() int      { return len(l) }
func (l tagsBySize) Swap(i, j int) { l[i], l[j] = l[j], l[i] }
func (l tagsBySize) Less(i, j int) bool {
	a, b := l[i], l[j]
	// Sort single-tag entries based on size first. Otherwise alphabetic.
	if len(a) != len(b) && (len(a) <= 4 || len(b) <= 4) {
		return len(a) < len(b)
	}
	return a < b
}

func (b *builder) writeSupported() {
	fmt.Printf("const numSupported = %d\n", len(b.supported))
	fmt.Print("const supported = \"\" +\n\t\"")
	n := 0
	for _, t := range b.supported {
		s := t.String()
		if n += len(s) + 1; n > 80 {
			n = len(s) + 1
			fmt.Print("\" + \n\t\"")
		}
		fmt.Printf("%s|", s)
	}
	fmt.Println("\"\n")
}

// parentIndices returns slice a of len(tags) where tags[a[i]] is the parent
// of tags[i].
func parentIndices(tags []language.Tag) []int {
	index := make(map[language.Tag]int)
	for i, t := range tags {
		index[t] = int(i)
	}

	// Construct default parents.
	parents := make([]int, len(tags))
	for i, t := range tags {
		parents[i] = -1
		for t = t.Parent(); t != language.Und; t = t.Parent() {
			if j, ok := index[t]; ok {
				parents[i] = j
				break
			}
		}
	}
	return parents
}

func (b *builder) writeParents() int {
	parents := parentIndices(b.supported)

	fmt.Printf("// parent relationship: %d entries\n", len(parents))
	fmt.Printf("var parents = [%d]int16{", len(parents))
	for i, v := range parents {
		if i%12 == 0 {
			fmt.Print("\n\t")
		}
		fmt.Printf("%d, ", v)
	}
	fmt.Println("}\n")
	return len(parents) * 2
}

// writeKeys writes keys to a special index used by the display package.
// tags are assumed to be sorted by length.
func writeKeys(name string, keys []string) (n int) {
	n = int(3 * reflect.TypeOf("").Size())
	fmt.Printf("// Number of keys: %d\n", len(keys))
	fmt.Printf("var (\n\t%sIndex = tagIndex{\n", name)
	for i := 2; i <= 4; i++ {
		sub := []string{}
		for _, t := range keys {
			if len(t) != i {
				break
			}
			sub = append(sub, t)
		}
		s := strings.Join(sub, "")
		n += len(s)
		fmt.Printf("\t\t%+q,\n", s)
		keys = keys[len(sub):]
	}
	fmt.Println("\t}")
	if len(keys) > 0 {
		fmt.Printf("\t%sTagsLong = %#v\n", name, keys)
		n += len(keys) * int(reflect.TypeOf("").Size())
		n += len(strings.Join(keys, ""))
		n += int(reflect.TypeOf([]string{}).Size())
	}
	fmt.Println(")\n")
	return n
}

func writeString(s string) {
	k := 0
	fmt.Print("\t\t\"")
	for _, r := range s {
		fmt.Print(string(r))
		if k++; k == 80 {
			fmt.Print("\" +\n\t\t\"")
			k = 0
		}
	}
	fmt.Print(`"`)
}

func writeUint16Body(a []uint16) {
	for v := a; len(v) > 0; {
		vv := v
		const nPerLine = 12
		if len(vv) > nPerLine {
			vv = v[:nPerLine]
			v = v[nPerLine:]
		} else {
			v = nil
		}
		fmt.Printf("\t\t\t")
		for _, x := range vv {
			fmt.Printf("0x%x, ", x)
		}
		fmt.Println()
	}
}

// identifier creates an identifier from the given tag.
func identifier(t language.Tag) string {
	return strings.Replace(t.String(), "-", "", -1)
}

func (h *header) writeEntry(name string) int {
	n := int(reflect.TypeOf(h.data).Size())
	n += int(reflect.TypeOf(h.index).Size())
	n += len(h.data)
	n += len(h.index) * 2

	if len(dict) > 0 && dict.contains(h.tag) {
		fmt.Printf("\t{ // %s\n", h.tag)
		fmt.Printf("\t\t%[1]s%[2]sStr,\n\t\t%[1]s%[2]sIdx,\n", identifier(h.tag), name)
		n += int(reflect.TypeOf(h.index).Size())
		fmt.Println("\t},")
	} else if len(h.data) == 0 {
		fmt.Println("\t\t{}, //", h.tag)
	} else {
		fmt.Printf("\t{ // %s\n", h.tag)
		writeString(h.data)
		fmt.Println(",")

		fmt.Printf("\t\t[]uint16{ // %d entries\n", len(h.index))
		writeUint16Body(h.index)
		fmt.Println("\t\t},")
		fmt.Println("\t},")
	}

	return n
}

// write the data for the given header as single entries. The size for this data
// was already accounted for in writeEntry.
func (h *header) writeSingle(name string) {
	if len(dict) > 0 && dict.contains(h.tag) {
		tag := identifier(h.tag)
		fmt.Printf("const %s%sStr = \"\" +\n", tag, name)
		writeString(h.data)
		fmt.Println("\n")

		// Note that we create a slice instead of an array. If we use an array
		// we need to refer to it as a[:] in other tables, which will cause the
		// array to always be included by the linker. See Issue 7651.
		fmt.Printf("var %s%sIdx = []uint16{ // %d entries\n", tag, name, len(h.index))
		writeUint16Body(h.index)
		fmt.Println("}\n")
	}
}

// WriteTable writes an entry for a single Namer.
func (g *group) writeTable(name string) int {
	n := writeKeys(name, g.toTags)
	fmt.Printf("var %sHeaders = [%d]header{\n", name, len(g.headers))

	title := strings.Title(name)
	for _, h := range g.headers {
		n += h.writeEntry(title)
	}
	fmt.Println("}\n")

	for _, h := range g.headers {
		h.writeSingle(title)
	}

	fmt.Printf("// Total size for %s: %d bytes (%d KB)\n\n", name, n, n/1000)
	return n
}

func (b *builder) writeDictionaries() int {
	fmt.Println("// Dictionary entries of frequent languages")
	fmt.Println("var (")
	parents := parentIndices(b.supported)

	for i, t := range b.supported {
		if dict.contains(t) {
			ident := identifier(t)
			fmt.Printf("\t%s = Dictionary{ // %s\n", ident, t)
			if p := parents[i]; p == -1 {
				fmt.Println("\t\tnil,")
			} else {
				fmt.Printf("\t\t&%s,\n", identifier(b.supported[p]))
			}
			fmt.Printf("\t\theader{%[1]sLangStr, %[1]sLangIdx},\n", ident)
			fmt.Printf("\t\theader{%[1]sScriptStr, %[1]sScriptIdx},\n", ident)
			fmt.Printf("\t\theader{%[1]sRegionStr, %[1]sRegionIdx},\n", ident)
			fmt.Println("\t}")
		}
	}
	fmt.Println(")")

	var s string
	var a []uint16
	sz := reflect.TypeOf(s).Size()
	sz += reflect.TypeOf(a).Size()
	sz *= 3
	sz += reflect.TypeOf(&a).Size()
	n := int(sz) * len(dict)
	fmt.Printf("// Total size for %d entries: %d bytes (%d KB)\n\n", len(dict), n, n/1000)

	return n
}

// unique sorts the given lists and removes duplicate entries by swapping them
// past position k, where k is the number of unique values. It returns k.
func unique(a sort.Interface) int {
	if a.Len() == 0 {
		return 0
	}
	sort.Sort(a)
	k := 1
	for i := 1; i < a.Len(); i++ {
		if a.Less(k-1, i) {
			if k != i {
				a.Swap(k, i)
			}
			k++
		}
	}
	return k
}

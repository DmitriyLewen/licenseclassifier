// Copyright 2020 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package classifier provides the implementation of the v2 license classifier.
package classifier

import (
	"fmt"
	"os"
	"strings"
)

type tokenID int // type to ensure safety when manipulating token identifiers.

// token provides detailed information about a single textual token in the document.
type token struct {
	Text     string // normalized text of the token
	Index    int    // the token's location in the tokenized document
	Line     int    // line position of this token in the source
	Previous string // for the first token in a line, any previous text.
}

// document is the representation of the input text for downstream filtering and matching.
type document struct {
	Tokens []*token // ordered tokens of the document
}

type indexedToken struct {
	Index int     // the token's location in the tokenized document
	Line  int     // line position of this token in the source
	ID    tokenID // identifier of the text in the dictionary
}

type IndexedDocument struct {
	Tokens []indexedToken  // ordered tokens of the document
	f      *FrequencyTable // frequencies computed for this document
	dict   *Dictionary     // The corpus dictionary for this document
	s      *SearchSet      // The searchset for this document
	runes  []rune
	norm   string // The normalized token sequence
}

func (d *IndexedDocument) generateSearchSet(q int) {
	d.s = newSearchSet(d, q)
}

func (d *IndexedDocument) size() int {
	return len(d.Tokens)
}

// normalized returns a string of the normalized tokens concatenated with a
// single space. This is used by the diff algorithm.
// TODO: it'd be more efficient to have the diff algorithm work with the raw tokens directly
// and avoid these ephemeral allocations.
func (d *IndexedDocument) normalized() string {
	var w strings.Builder
	for i, t := range d.Tokens {
		w.WriteString(d.dict.getWord(t.ID))
		if (i + 1) != d.size() {
			w.WriteString(" ")
		}
	}
	return w.String()
}

func computeQ(threshold float64) int {
	// q is the lower bound for token runs (q-grams) that must exist
	// in content that can be recognized at the specified threshold.
	// Imagine a document with 100 tokens, and a threshold of 80%. This means
	// that in a worst-case scenario, the 20 errors are evenly distributed to
	// create the sortest possible token runs. In this case, there would be
	// a repeating sequence of 4 good tokens and 1 errored token, occurring
	// 20 times. This function returns the minimum token length, or returning
	// a value of 1 if necessary (since a threshold level below 50% would generate
	// a run of 0-length, which is meaningless.)
	if threshold == 1.0 {
		return 10 // avoid divide by 0
	}

	return max(1, int((threshold)/(1.0-threshold)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AddContent incorporates the provided textual content into the classifier for
// matching. This will not modify the supplied content.
func (c *Classifier) AddContent(category, name, variant string, content []byte) {
	doc := tokenize(content)
	c.addDocument(category, name, variant, doc)
}

// addDocument takes a textual document and incorporates it into the classifier for matching.
func (c *Classifier) addDocument(category, name, variant string, doc *document) {
	// For documents that are part of the corpus, we add them to the dictionary and
	// compute their associated search data eagerly so they are ready for matching against
	// candidates.
	indexName := c.generateDocName(category, name, variant)
	id := c.generateIndexedDocument(doc, true)
	id.generateFrequencies()
	id.generateSearchSet(c.Q)
	id.s.origin = indexName
	c.Docs[indexName] = id
}

// generateIndexedDocument creates an IndexedDocument from the supplied document. if addWords
// is true, the classifier dictionary is updated with new tokens encountered in the document.
func (c *Classifier) generateIndexedDocument(d *document, addWords bool) *IndexedDocument {
	id := &IndexedDocument{
		Tokens: make([]indexedToken, 0, len(d.Tokens)),
		dict:   c.Dict,
	}

	for _, t := range d.Tokens {
		var tokID tokenID
		if addWords {
			tokID = id.dict.add(t.Text)
		} else {
			tokID = id.dict.getIndex(t.Text)
		}

		id.Tokens = append(id.Tokens, indexedToken{
			Index: t.Index,
			Line:  t.Line,
			ID:    tokID,
		})

	}
	id.generateFrequencies()
	id.runes = diffWordsToRunes(id, 0, id.size())
	id.norm = id.normalized()
	return id
}

// createTargetIndexedDocument creates an indexed document without adding the
// words to the classifier dictionary. This should be used for matching targets, not
// populating the corpus.
func (c *Classifier) createTargetIndexedDocument(in []byte) *IndexedDocument {
	doc := tokenize(in)
	return c.generateIndexedDocument(doc, false)
}

func (c *Classifier) generateDocName(category, name, variant string) string {
	return fmt.Sprintf("%s%c%s%c%s", category, os.PathSeparator, name, os.PathSeparator, variant)
}
func (c *Classifier) getIndexedDocument(category, name, variant string) *IndexedDocument {
	return c.Docs[c.generateDocName(category, name, variant)]
}

// dictionary is used to intern all the token words encountered in the text corpus.
// words and indices form an inverse mapping relationship. It is just a convenience type
// over a pair of correlated maps.
type Dictionary struct {
	Words   map[tokenID]string
	Indices map[string]tokenID
}

func newDictionary() *Dictionary {
	return &Dictionary{
		Words:   make(map[tokenID]string),
		Indices: make(map[string]tokenID),
	}
}

// add inserts the provided word into the dictionary if it does not already exist.
func (d *Dictionary) add(word string) tokenID {
	if idx := d.getIndex(word); idx != unknownIndex {
		return idx
	}
	// token IDs start from 1, 0 is reserved for the invalid ID
	idx := tokenID(len(d.Words) + 1)
	d.Words[idx] = word
	d.Indices[word] = idx
	return idx
}

var unknownWord = "UNKNOWN"
var unknownIndex = tokenID(0)

// getIndex returns the index of the supplied word, or 0 if the word is not in the dictionary.
func (d *Dictionary) getIndex(word string) tokenID {
	if idx, found := d.Indices[word]; found {
		return idx
	}
	return unknownIndex
}

// getWord returns the word associated with the index.
func (d *Dictionary) getWord(index tokenID) string {
	if word, found := d.Words[index]; found {
		return word
	}
	return unknownWord
}

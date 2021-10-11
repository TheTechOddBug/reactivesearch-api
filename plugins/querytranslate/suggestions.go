package querytranslate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/appbaseio/reactivesearch-api/util"
	"github.com/microcosm-cc/bluemonday"
	log "github.com/sirupsen/logrus"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Do this once for each unique policy, and use the policy for the life of the program
// Policy creation/editing is not safe to use in multiple goroutines
var p = bluemonday.StrictPolicy()

type SuggestionType int

const (
	Index SuggestionType = iota
	Popular
	Recent
	Promoted
)

// String is the implementation of Stringer interface that returns the string representation of SuggestionType type.
func (o SuggestionType) String() string {
	return [...]string{
		"index",
		"popular",
		"recent",
		"promoted",
	}[o]
}

// UnmarshalJSON is the implementation of the Unmarshaler interface for unmarshaling SuggestionType type.
func (o *SuggestionType) UnmarshalJSON(bytes []byte) error {
	var suggestionType string
	err := json.Unmarshal(bytes, &suggestionType)
	if err != nil {
		return err
	}
	switch suggestionType {
	case Index.String():
		*o = Index
	case Popular.String():
		*o = Popular
	case Recent.String():
		*o = Recent
	case Promoted.String():
		*o = Promoted
	default:
		return fmt.Errorf("invalid suggestion type encountered: %v", suggestionType)
	}
	return nil
}

// MarshalJSON is the implementation of the Marshaler interface for marshaling SuggestionType type.
func (o SuggestionType) MarshalJSON() ([]byte, error) {
	var suggestionType string
	switch o {
	case Index:
		suggestionType = Index.String()
	case Popular:
		suggestionType = Popular.String()
	case Recent:
		suggestionType = Recent.String()
	case Promoted:
		suggestionType = Promoted.String()
	default:
		return nil, fmt.Errorf("invalid suggestion type encountered: %v", o)
	}
	return json.Marshal(suggestionType)
}

// SuggestionHIT represents the structure of the suggestion object in RS API response
type SuggestionHIT struct {
	Value    string         `json:"value"`
	Label    string         `json:"label"`
	URL      *string        `json:"url"`
	Type     SuggestionType `json:"_suggestion_type"`
	Category *string        `json:"_category"`
	Count    *int           `json:"_count"`
	// ES response properties
	Id     interface{}            `json:"_id"`
	Index  *string                `json:"_index"`
	Score  *float64               `json:"_score"`
	Source map[string]interface{} `json:"_source"`
}

type SuggestionHitResponse struct {
	Total    interface{}     `json:"total"`
	MaxScore interface{}     `json:"max_score"`
	Hits     []SuggestionHIT `json:"hits"`
}

// Response of the suggestions API similar to the ES response
type SuggestionESResponse struct {
	Took int                   `json:"took"`
	Hits SuggestionHitResponse `json:"hits"`
}

// RecentSuggestionsOptions represents the options to configure recent suggestions
type RecentSuggestionsOptions struct {
	Size     *int    `json:"size,omitempty"`
	Index    *string `json:"index,omitempty"`
	MinHits  *int    `json:"minHits,omitempty"`
	MinChars *int    `json:"minChars,omitempty"`
}

// PopularSuggestionsOptions represents the options to configure popular suggestions
type PopularSuggestionsOptions struct {
	Size       *int    `json:"size,omitempty"`
	Index      *string `json:"index,omitempty"`
	ShowGlobal *bool   `json:"showGlobal,omitempty"`
}

type SuggestionsConfig struct {
	// Data fields to parse suggestions.
	// If not defined then we'll extract fields from source object.
	DataFields []string
	// Query value
	Value                       string
	ShowDistinctSuggestions     *bool
	EnablePredictiveSuggestions *bool
	MaxPredictedWords           *int
	EnableSynonyms              *bool
	ApplyStopwords              *bool
	Stopwords                   *[]string
	URLField                    *string
	CategoryField               *string
}

func isMn(r rune) bool {
	return unicode.Is(unicode.Mn, r) // Mn: nonspacing marks
}

func replaceDiacritics(query string) string {
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	queryKey, _, _ := transform.String(t, query)
	return queryKey
}

type SuggestionInfo struct {
	value         string
	skipWordMatch bool
	source        map[string]interface{}
	urlField      *string
	categoryField *string
	rawHit        ESDoc
}

func populateSuggestionsList(
	labelsList *[]string,
	suggestionsList *[]SuggestionHIT,
	suggestionsInfo SuggestionInfo,
) bool {

	// check if the suggestion includes the current value
	// and not already included in other suggestions
	isWordMatch := suggestionsInfo.skipWordMatch
	// find match
	for _, value := range strings.Split(strings.Trim(suggestionsInfo.value, " "), " ") {
		if strings.Contains(strings.ToLower(replaceDiacritics(suggestionsInfo.value)), replaceDiacritics(value)) {
			isWordMatch = true
			break
		}
	}
	if isWordMatch && !util.Contains(*labelsList, suggestionsInfo.value) {
		var url string
		if suggestionsInfo.urlField != nil {
			urlString, ok := suggestionsInfo.rawHit.Source[*suggestionsInfo.urlField].(string)
			if ok {
				url = urlString
			}
		}
		var category string
		if suggestionsInfo.categoryField != nil {
			categoryString, ok := suggestionsInfo.rawHit.Source[*suggestionsInfo.categoryField].(string)
			if ok {
				category = categoryString
			}
		}
		suggestion := SuggestionHIT{
			Value:    getTextFromHTML(suggestionsInfo.value),
			Label:    suggestionsInfo.value,
			URL:      &url,
			Type:     Index,
			Category: &category,
			// ES response properties
			Id:     &suggestionsInfo.rawHit.Id,
			Index:  &suggestionsInfo.rawHit.Index,
			Source: suggestionsInfo.rawHit.Source,
			Score:  &suggestionsInfo.rawHit.Score,
		}

		*labelsList = append(*labelsList, suggestionsInfo.value)
		*suggestionsList = append(*suggestionsList, suggestion)
		return false
	}
	return false
}

// extracts the string from HTML tags
func getTextFromHTML(body string) string {

	// The policy can then be used to sanitize lots of input and it is safe to use the policy in multiple goroutines
	html := p.Sanitize(
		body,
	)

	return html
}

func getPredictiveSuggestions(config SuggestionsConfig, suggestions *[]SuggestionHIT) []SuggestionHIT {
	var suggestionsList = make([]SuggestionHIT, 0)
	var suggestionsMap = make(map[string]bool)
	if config.Value != "" {
		currentValueTrimmed := strings.Trim(config.Value, " ")
		for _, suggestion := range *suggestions {
			// to handle special strings with pattern <mark>xyz</mark>
			// extract the raw text from the highlighted value
			parsedContent := getTextFromHTML(suggestion.Label)
			// to match the partial start of word.
			// example if searchTerm is `select` and string contains `selected`
			regex, err := regexp.Compile("(?i)" + regexp.QuoteMeta(currentValueTrimmed))
			if err != nil {
				log.Warnln(logTag, ":", err.Error())
				continue
			}
			matchIndex := regex.FindStringIndex(parsedContent)
			// if not matchIndex not present then it means either there is no match or there are chances
			// that exact word is present
			if matchIndex == nil {
				// match with exact word
				regex2, err2 := regexp.Compile("(?i)" + "^" + regexp.QuoteMeta(currentValueTrimmed))
				if err2 != nil {
					log.Warnln(logTag, ":", err2.Error())
					continue
				}
				matchIndex = regex2.FindStringIndex(parsedContent)
			}
			if matchIndex != nil && len(parsedContent) > matchIndex[0] {
				matchedString := parsedContent[matchIndex[0]:]
				suffixWords := strings.Split(matchedString[len(currentValueTrimmed):], " ")
				prefixWords := strings.Split(parsedContent[:matchIndex[0]], " ")
				maxPredictedWords := 2
				if config.MaxPredictedWords != nil {
					maxPredictedWords = *config.MaxPredictedWords
				}
				matched := false
				stopwordsToApply := stopwords
				// use custom stopwords if present
				if config.Stopwords != nil {
					stopwordsToApply = make(map[string]bool)
					for _, v := range *config.Stopwords {
						stopwordsToApply[v] = true
					}
				}
				// apply suffix match
				if len(suffixWords) > 0 {
					for i := maxPredictedWords + 1; i > 0; i-- {
						// find the longest match
						if i <= len(suffixWords) && !matched {
							highlightedWord := strings.Join(suffixWords[:i], " ")
							if strings.Trim(highlightedWord, "") != "" &&
								len(strings.Split(highlightedWord, " ")) <= maxPredictedWords+1 {
								// a prefix shouldn't be a stopword
								if config.ApplyStopwords != nil && *config.ApplyStopwords {
									lastWord := strings.Trim(suffixWords[:i][len(suffixWords[:i])-1], " ")
									if stopwordsToApply[lastWord] {
										continue
									}
								}
								suggestionPhrase := currentValueTrimmed + `<mark class="highlight">` + highlightedWord + `</mark>`
								suggestionValue := currentValueTrimmed + highlightedWord
								matched = true
								// to show unique results only
								if !suggestionsMap[suggestionPhrase] {
									predictiveSuggestion := suggestion
									predictiveSuggestion.Label = strings.ToLower(suggestionPhrase)
									predictiveSuggestion.Value = strings.ToLower(suggestionValue)
									suggestionsList = append(suggestionsList, predictiveSuggestion)
									// update map
									suggestionsMap[suggestionPhrase] = true
								}
							}
						}
					}
				}
				// apply prefix match
				if !matched && len(prefixWords) > 0 {
					for i := maxPredictedWords + 1; i >= 0; i-- {
						// find the shortest match
						if i <= len(prefixWords) && !matched {
							highlightedWord := strings.Join(prefixWords[i:], " ")
							if strings.Trim(highlightedWord, "") != "" && len(strings.Split(highlightedWord, " ")) <= maxPredictedWords+1 {
								// a prefix shouldn't be a stopword
								if config.ApplyStopwords != nil && *config.ApplyStopwords {
									firstWord := strings.Trim(prefixWords[i:][0], " ")
									if stopwordsToApply[firstWord] {
										continue
									}
								}
								suggestionPhrase := `<mark class="highlight">` + highlightedWord + `</mark>` + currentValueTrimmed
								suggestionValue := highlightedWord + currentValueTrimmed
								matched = true
								// to show unique results only
								if !suggestionsMap[suggestionPhrase] {
									predictiveSuggestion := suggestion
									predictiveSuggestion.Label = strings.ToLower(suggestionPhrase)
									predictiveSuggestion.Value = strings.ToLower(suggestionValue)
									suggestionsList = append(suggestionsList, predictiveSuggestion)
									// update map
									suggestionsMap[suggestionPhrase] = true
								}
							}

						}
					}
				}
			}
		}
	}
	return suggestionsList
}

// Parse the index suggestions from the source object
func getSuggestions(config SuggestionsConfig, rawHits []ESDoc) []SuggestionHIT {

	// keep track of suggestions list
	var suggestionsList = make([]SuggestionHIT, 0)

	// keep track of suggestions label, label must be unique
	var labelsList = make([]string, 0)

	traverseSuggestions(config, rawHits, false, &suggestionsList, &labelsList)

	if len(suggestionsList) < len(rawHits) && (config.EnableSynonyms == nil || *config.EnableSynonyms) {
		// 	When we have synonym we set skipWordMatch to false as it may discard
		// 	the suggestion if word doesnt match term.
		// 	For eg: iphone, ios are synonyms and on searching iphone isWordMatch
		// 	in  populateSuggestionList may discard ios source which decreases no.
		// 	of items in suggestionsList
		traverseSuggestions(config, rawHits, true, &suggestionsList, &labelsList)
	}
	if config.EnablePredictiveSuggestions != nil && *config.EnablePredictiveSuggestions {
		suggestionsList = getPredictiveSuggestions(config, &suggestionsList)
	}

	if config.ShowDistinctSuggestions != nil && *config.ShowDistinctSuggestions {
		// keep track of document ids for suggestions
		var idMap = make(map[interface{}]bool)
		filteredSuggestions := make([]SuggestionHIT, 0)
		for _, suggestion := range suggestionsList {
			if suggestion.Id != nil {
				if !idMap[suggestion.Id] {
					filteredSuggestions = append(filteredSuggestions, suggestion)
					idMap[suggestion.Id] = true
				}
			}
		}
		return filteredSuggestions
	}
	return suggestionsList
}

func flattenDeep(args []interface{}, v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		for _, v := range s {
			args = flattenDeep(args, v)
		}
	} else {
		args = append(args, v)
	}
	return args
}

func extractSuggestion(val interface{}) interface{} {
	valString, ok1 := val.(string)
	if ok1 {
		return valString
	}
	valArray, ok2 := val.([]interface{})
	if ok2 {
		return flattenDeep(nil, valArray)
	}
	_, ok3 := val.(map[string]interface{})
	if ok3 {
		return nil
	}
	return val
}

func parseField(
	source map[string]interface{},
	field string,
	skipWordMatch bool,
	suggestionsList *[]SuggestionHIT,
	labelsList *[]string,
	rawHit ESDoc,
	config SuggestionsConfig,
) bool {
	fieldNodes := strings.Split(field, ".")
	label := source[fieldNodes[0]]
	// To handle field names with dots
	// For example, if source has a top level field name is `user.name`
	// then it would extract the suggestion from parsed source

	if source[field] != nil {
		topLabel := source[field]
		val := extractSuggestion(topLabel)
		valAsString, ok := val.(string)
		if ok && valAsString != "" {
			suggestionInfo := SuggestionInfo{
				value:         valAsString,
				skipWordMatch: skipWordMatch,
				source:        source,
				urlField:      config.URLField,
				categoryField: config.CategoryField,
				rawHit:        rawHit,
			}
			return populateSuggestionsList(labelsList, suggestionsList, suggestionInfo)
		}
	}
	// if the type of field is array of strings
	// then we need to pick first matching value as the label
	labelAsArray, ok := label.([]interface{})
	if ok && len(labelAsArray) > 1 {
		var matchedLabel []interface{}
		for _, i := range labelAsArray {
			labelAsString, ok := i.(string)
			// find the matching label
			if ok && strings.Contains(strings.ToLower(labelAsString), strings.ToLower(config.Value)) {
				matchedLabel = append(matchedLabel, labelAsString)
			}
		}
		if len(matchedLabel) > 0 {
			label = matchedLabel[0]
		}
	}

	if label != nil {
		if len(fieldNodes) > 1 {
			// nested fields of the 'foo.bar.zoo' variety
			children := field[len(fieldNodes[0])+1:]
			labelAsMap, ok := label.(map[string]interface{})
			if ok {
				parseField(labelAsMap, children, skipWordMatch, suggestionsList, labelsList, rawHit, config)
			}
		} else {
			val := extractSuggestion(label)
			valAsString, ok := val.(string)
			if ok {
				suggestionInfo := SuggestionInfo{
					value:         valAsString,
					skipWordMatch: skipWordMatch,
					source:        source,
					urlField:      config.URLField,
					categoryField: config.CategoryField,
					rawHit:        rawHit,
				}
				return populateSuggestionsList(labelsList, suggestionsList, suggestionInfo)
			}
		}
	}
	return false
}

func traverseSuggestions(
	config SuggestionsConfig,
	suggestions []ESDoc,
	skipWordMatch bool,
	suggestionsList *[]SuggestionHIT,
	labelsList *[]string,
) {
	for _, suggestion := range suggestions {
		for _, field := range config.DataFields {
			parseField(suggestion.ParsedSource, field, skipWordMatch, suggestionsList, labelsList, suggestion, config)
		}
	}
}

type ESDoc struct {
	Index        string                 `json:"_index"`
	Type         string                 `json:"type"`
	Id           string                 `json:"_id"`
	Score        float64                `json:"_score"`
	Source       map[string]interface{} `json:"_source"`
	Highlight    map[string]interface{} `json:"highlight"`
	ParsedSource map[string]interface{}
}

// Highlights the fields by replacing the actual value with markup
func highlightResults(source ESDoc) ESDoc {
	source.ParsedSource = make(map[string]interface{})
	// clone map
	for k, v := range source.Source {
		source.ParsedSource[k] = v
	}

	if source.Highlight != nil {
		for highlightItem, highlightedValue := range source.Highlight {
			highlightValueArray, ok := highlightedValue.([]interface{})
			if ok && len(highlightValueArray) > 0 {
				highlightValue := highlightValueArray[0]
				source.ParsedSource[highlightItem] = highlightValue
			}
		}
	}
	return source
}

// To parse the elasticsearch hits with highlighted fields
func parseHits(hits []ESDoc) []ESDoc {
	var results = make([]ESDoc, 0)
	for _, hit := range hits {
		results = append(results, highlightResults(hit))
	}
	return results
}

// Util method to extract the fields from elasticsearch source object
// It can handle nested objects and arrays too.
// Example 1:
// Input: { a: 1, b: { b_1: 2, b_2: 3}}
// Output: ['a', 'b.b_1', 'b.b_2']
// Example 2:
// Input: { a: 1, b: [{c: 1}, {d: 2}, {c: 3}]}
// Output: ['a', 'b.c', 'b.d']
func getFields(source interface{}, prefix string) map[string]interface{} {
	dataFields := make(map[string]interface{})
	sourceAsMap, ok := source.(map[string]interface{})
	if ok {
		for field := range sourceAsMap {
			var key string
			if prefix != "" {
				key = prefix + "." + field
			} else {
				key = field
			}
			if sourceAsMap[field] != nil {
				mapValue, ok := sourceAsMap[field].(map[string]interface{})
				if ok {
					mergeMaps(dataFields, getFields(mapValue, key))
				} else {
					mapValueAsArray, ok := sourceAsMap[field].([]interface{})
					if ok {
						mergeMaps(dataFields, getFields(mapValueAsArray, key))
					} else {
						mergeMaps(dataFields, map[string]interface{}{
							key: true,
						})
					}
				}
			}
		}
	} else {
		sourceAsArray, ok := source.([]interface{})
		if ok {
			for field := range sourceAsArray {
				var key string
				if prefix != "" {
					key = prefix
				} else {
					key = strconv.Itoa(field)
				}
				if sourceAsArray[field] != nil {
					mapValue, ok := sourceAsArray[field].(map[string]interface{})
					if ok {
						mergeMaps(dataFields, getFields(mapValue, key))
					} else {
						mapValueAsArray, ok := sourceAsArray[field].([]interface{})
						if ok {
							mergeMaps(dataFields, getFields(mapValueAsArray, key))
						} else {
							mergeMaps(dataFields, map[string]interface{}{
								key: true,
							})
						}
					}
				}
			}
		}
	}

	return dataFields
}

func extractFieldsFromSource(source map[string]interface{}) []string {
	dataFields := []string{}
	var sourceAsInterface interface{} = source
	dataFieldsMap := getFields(sourceAsInterface, "")
	for k := range dataFieldsMap {
		dataFields = append(dataFields, k)
	}
	return dataFields
}

func getFinalSuggestions(config SuggestionsConfig, rawHits []ESDoc) []SuggestionHIT {
	// extract dataFields
	if len(config.DataFields) == 0 && len(rawHits) > 0 {
		// extract fields from first hit source
		config.DataFields = extractFieldsFromSource(rawHits[0].Source)
	}
	// parse hits
	parsedHits := parseHits(rawHits)
	// TODO: Restrict length by size
	return getSuggestions(config, parsedHits)
}
package querytranslate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/appbaseio/reactivesearch-api/middleware/classify"
	"github.com/appbaseio/reactivesearch-api/model/index"
	"github.com/appbaseio/reactivesearch-api/util"
	"github.com/buger/jsonparser"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func (r *QueryTranslate) search() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		vars := mux.Vars(req)
		reqBody, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, "Can't read request body", http.StatusBadRequest)
			return
		}
		defer req.Body.Close()
		reqURL := "/" + vars["index"] + "/_msearch"
		start := time.Now()
		httpRes, err := makeESRequest(ctx, reqURL, http.MethodPost, reqBody)
		if err != nil {
			msg := err.Error()
			log.Errorln(logTag, ":", err)
			// Response can be nil sometimes
			if httpRes != nil {
				util.WriteBackError(w, msg, httpRes.StatusCode)
				return
			}
			util.WriteBackError(w, msg, http.StatusInternalServerError)
			return
		}
		log.Println("TIME TAKEN BY ES:", time.Since(start))
		if httpRes.StatusCode > 500 {
			msg := "unable to connect to the upstream Elasticsearch cluster"
			log.Errorln(logTag, ":", msg)
			util.WriteBackError(w, msg, httpRes.StatusCode)
			return
		}
		rsAPIRequest, err := FromContext(req.Context())
		if err != nil {
			msg := "error occurred while retrieving request body from context"
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, msg, http.StatusInternalServerError)
			return
		}

		queryIds := getQueryIds(*rsAPIRequest)

		rsResponse := []byte(`{}`)

		took, valueType1, _, err := jsonparser.Get(httpRes.Body, "took")
		// ignore not exist error
		if err != nil && valueType1 != jsonparser.NotExist {
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, "can't parse took key from response", http.StatusInternalServerError)
			return
		}
		// Set the `settings` key to response
		rsResponseWithTook, err := jsonparser.Set(rsResponse, []byte(fmt.Sprintf(`{ "took": %s }`, string(took))), "settings")
		if err != nil {
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, "can't add settings key to response", http.StatusInternalServerError)
			return
		}
		// Assign updated json to actual response
		rsResponse = rsResponseWithTook

		responseError, valueType2, _, err := jsonparser.Get(httpRes.Body, "error")
		// ignore not exist error
		if err != nil && valueType2 != jsonparser.NotExist {
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, "can't parse error key from response", http.StatusInternalServerError)
			return
		} else if responseError != nil {
			// Set the `error` key to response
			rsResponseWithError, err := jsonparser.Set(rsResponse, responseError, "error")
			if err != nil {
				log.Errorln(logTag, ":", err)
				util.WriteBackError(w, "can't add error key to response", http.StatusInternalServerError)
				return
			}
			// Assign updated json to actual response
			rsResponse = rsResponseWithError
		}

		// Read `responses` value from the response body
		responses, valueType3, _, err4 := jsonparser.Get(httpRes.Body, "responses")
		// ignore not exist error
		if err4 != nil && valueType3 != jsonparser.NotExist {
			log.Errorln(logTag, ":", err4)
			util.WriteBackError(w, "can't parse responses key from response", http.StatusInternalServerError)
			return
		}

		if responses != nil {
			index := 0
			// Set `responses` by query ID
			jsonparser.ArrayEach(responses, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
				queryID := queryIds[index]
				var isSuggestionRequest bool
				var suggestions = make([]SuggestionHIT, 0)
				// parse suggestions if query is of type `suggestion`
				for _, query := range rsAPIRequest.Query {
					if *query.ID == queryID && query.Type == Suggestion {
						isSuggestionRequest = true
						// Index suggestions are not meant for empty query
						if query.Value != nil {
							valueAsString, ok := (*query.Value).(string)
							if ok {
								var normalizedDataFields = []string{}
								normalizedFields := NormalizedDataFields(query.DataField, query.FieldWeights)
								for _, dataField := range normalizedFields {
									normalizedDataFields = append(normalizedDataFields, dataField.Field)
								}
								suggestionsConfig := SuggestionsConfig{
									// Fields to extract suggestions
									DataFields: normalizedDataFields,
									// Query value
									Value:                       valueAsString,
									ShowDistinctSuggestions:     query.ShowDistinctSuggestions,
									EnablePredictiveSuggestions: query.EnablePredictiveSuggestions,
									MaxPredictedWords:           query.MaxPredictedWords,
									EnableSynonyms:              query.EnableSynonyms,
								}

								var rawHits []ESDoc
								hits, dataType, _, err1 := jsonparser.Get(value, "hits", "hits")
								if dataType != jsonparser.NotExist && err1 != nil {
									log.Errorln(logTag, ":", err1)
									util.WriteBackError(w, "error while retriving hits: "+err1.Error(), http.StatusInternalServerError)
									return
								}
								err := json.Unmarshal(hits, &rawHits)
								if err != nil {
									log.Errorln(logTag, ":", err)
									util.WriteBackError(w, "error while parsing ES response to hits: "+err.Error(), http.StatusInternalServerError)
									return
								}
								log.Println("RAW HITS", rawHits)
								suggestions = getFinalSuggestions(suggestionsConfig, rawHits)
							}
						}
					}
				}
				if isSuggestionRequest {
					responseInByte, err := json.Marshal(suggestions)
					if err != nil {
						log.Errorln(logTag, ":", err)
						util.WriteBackError(w, "error while parsing suggestions", http.StatusInternalServerError)
						return
					}
					rsResponseWithSuggestions, err := jsonparser.Set(value, responseInByte, "hits", "hits")
					if err != nil {
						log.Errorln(logTag, ":", err)
						util.WriteBackError(w, "can't add suggestions to final response", http.StatusInternalServerError)
						return
					}
					rsResponseWithSearchResponse, err := jsonparser.Set(rsResponse, rsResponseWithSuggestions, queryID)
					if err != nil {
						log.Errorln(logTag, ":", err)
						util.WriteBackError(w, "can't add search response to final response", http.StatusInternalServerError)
						return
					}
					rsResponse = rsResponseWithSearchResponse

				} else {
					rsResponseWithSearchResponse, err := jsonparser.Set(rsResponse, value, queryID)
					if err != nil {
						log.Errorln(logTag, ":", err)
						util.WriteBackError(w, "can't add search response to final response", http.StatusInternalServerError)
						return
					}
					rsResponse = rsResponseWithSearchResponse
				}
				index++
			})
		}

		indices, err := index.FromContext(req.Context())
		if err != nil {
			msg := "error getting the index names from context"
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, msg, http.StatusInternalServerError)
			return
		}
		// Replace indices to alias
		for _, index := range indices {
			alias := classify.GetIndexAlias(index)
			if alias != "" {
				rsResponse = bytes.Replace(rsResponse, []byte(`"`+index+`"`), []byte(`"`+alias+`"`), -1)
				continue
			}
			// if alias is present in url get index name from cache
			indexName := classify.GetAliasIndex(index)
			if indexName != "" {
				rsResponse = bytes.Replace(rsResponse, []byte(`"`+indexName+`"`), []byte(`"`+index+`"`), -1)
			}
		}
		// if status code is not 200 write rsResponse otherwise return raw response from ES
		// avoid copy for performance reasons
		if httpRes.StatusCode == http.StatusOK {
			util.WriteBackRaw(w, rsResponse, httpRes.StatusCode)
		} else {
			util.WriteBackRaw(w, httpRes.Body, httpRes.StatusCode)
		}
	}
}

func (r *QueryTranslate) validate() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		reqBody, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Errorln(logTag, ":", err)
			util.WriteBackError(w, "Can't read request body", http.StatusBadRequest)
			return
		}
		w.Header().Add("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(reqBody)))
	}
}

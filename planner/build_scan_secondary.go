//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package planner

import (
	"fmt"

	"github.com/couchbase/query/algebra"
	"github.com/couchbase/query/datastore"
	"github.com/couchbase/query/errors"
	"github.com/couchbase/query/expression"
	"github.com/couchbase/query/logging"
	"github.com/couchbase/query/plan"
	"github.com/couchbase/query/value"
)

func (this *builder) buildSecondaryScan(indexes map[datastore.Index]*indexEntry,
	node *algebra.KeyspaceTerm, baseKeyspace *baseKeyspace, id expression.Expression) (
	plan.SecondaryScan, int, error) {

	if this.cover != nil && !node.IsAnsiNest() {
		scan, sargLength, err := this.buildCoveringScan(indexes, node, baseKeyspace, id)
		if scan != nil || err != nil {
			return scan, sargLength, err
		}
	}

	this.resetProjection()
	if this.group != nil {
		this.resetPushDowns()
	}

	pred := baseKeyspace.dnfPred

	indexes = minimalIndexes(indexes, true, pred)

	var err error
	err = this.sargIndexes(baseKeyspace, node.IsUnderHash(), indexes)
	if err != nil {
		return nil, 0, err
	}

	var orderIndex datastore.Index
	var limit expression.Expression
	pushDown := false

	for _, entry := range indexes {
		entry.pushDownProperty = this.indexPushDownProperty(entry, entry.keys, nil, pred, node.Alias(), false, false)

		if this.order != nil && entry.IsPushDownProperty(_PUSHDOWN_ORDER) {
			orderIndex = entry.index
			this.maxParallelism = 1
		}

		if !pushDown && entry.IsPushDownProperty(_PUSHDOWN_LIMIT|_PUSHDOWN_OFFSET) {
			pushDown = true
		}
	}

	// No ordering index, disable ORDER and LIMIT pushdown
	if this.order != nil && orderIndex == nil {
		this.resetOrderOffsetLimit()
	}

	if pushDown && len(indexes) > 1 {
		limit = offsetPlusLimit(this.offset, this.limit)
		this.resetOffsetLimit()
	} else if !pushDown {
		this.resetOffsetLimit()
	}

	// Ordering scan, if any, will go into scans[0]
	var scanBuf [16]plan.SecondaryScan
	var scans []plan.SecondaryScan
	var scan plan.SecondaryScan
	var indexProjection *plan.IndexProjection
	sargLength := 0

	if len(indexes) <= len(scanBuf) {
		scans = scanBuf[0:1]
	} else {
		scans = make([]plan.SecondaryScan, 1, len(indexes))
	}

	if len(indexes) == 1 {
		for _, entry := range indexes {
			indexProjection = this.buildIndexProjection(entry, nil, nil, true)
			if this.offset != nil && !entry.IsPushDownProperty(_PUSHDOWN_OFFSET) {
				this.limit = offsetPlusLimit(this.offset, this.limit)
				this.resetOffset()
			}
			break
		}
	} else {
		indexProjection = this.buildIndexProjection(nil, nil, nil, true)
	}

	for index, entry := range indexes {
		// If this is a join with primary key (meta().id), then it's
		// possible to get right hand documdents directly without
		// accessing through an index (similar to "regular" join).
		// In such cases do not consider secondary indexes that does
		// not include meta().id as a sargable index key. In addition,
		// the index must have either a WHERE clause or at least
		// one other sargable key.
		if node.IsPrimaryJoin() {
			metaFound := false
			for _, key := range entry.sargKeys {
				if key.EquivalentTo(id) {
					metaFound = true
					break
				}
			}

			if !metaFound || (len(entry.sargKeys) <= 1 && index.Condition() == nil) {
				continue
			}
		}

		var indexKeyOrders plan.IndexKeyOrders
		if index == orderIndex {
			_, indexKeyOrders = this.useIndexOrder(entry, entry.keys)
		}

		scan = entry.spans.CreateScan(index, node, this.indexApiVersion, false, false, pred.MayOverlapSpans(), false,
			this.offset, this.limit, indexProjection, indexKeyOrders, nil, nil, nil)

		if index == orderIndex {
			scans[0] = scan
		} else {
			scans = append(scans, scan)
		}

		if len(entry.sargKeys) > sargLength {
			sargLength = len(entry.sargKeys)
		}
	}

	if len(scans) == 1 {
		this.orderScan = scans[0]
		return scans[0], sargLength, nil
	} else if scans[0] == nil && len(scans) == 2 {
		return scans[1], sargLength, nil
	} else if scans[0] == nil {
		return plan.NewIntersectScan(limit, scans[1:]...), sargLength, nil
	} else {
		scan = plan.NewOrderedIntersectScan(limit, scans...)
		this.orderScan = scan
		return scan, sargLength, nil
	}
}

func (this *builder) sargableIndexes(indexes []datastore.Index, pred, subset expression.Expression,
	primaryKey expression.Expressions, formalizer *expression.Formalizer) (
	sargables, all, arrays map[datastore.Index]*indexEntry, err error) {

	sargables = make(map[datastore.Index]*indexEntry, len(indexes))
	all = make(map[datastore.Index]*indexEntry, len(indexes))
	arrays = make(map[datastore.Index]*indexEntry, len(indexes))

	var keys expression.Expressions

	for _, index := range indexes {
		isArray := false

		if index.IsPrimary() {
			if primaryKey != nil {
				keys = primaryKey
			} else {
				continue
			}
		} else {
			keys = index.RangeKey()
			keys = keys.Copy()

			for i, key := range keys {
				key = key.Copy()

				formalizer.SetIndexScope()
				key, err = formalizer.Map(key)
				formalizer.ClearIndexScope()
				if err != nil {
					return
				}

				dnf := NewDNF(key, true, true)
				key, err = dnf.Map(key)
				if err != nil {
					return
				}

				keys[i] = key

				if !isArray {
					isArray, _ = key.IsArrayIndexKey()
				}
			}
		}

		var origCond expression.Expression
		cond := index.Condition()
		if cond != nil {
			if subset == nil {
				continue
			}

			cond = cond.Copy()

			formalizer.SetIndexScope()
			cond, err = formalizer.Map(cond)
			formalizer.ClearIndexScope()
			if err != nil {
				return
			}

			origCond = cond.Copy()

			dnf := NewDNF(cond, true, true)
			cond, err = dnf.Map(cond)
			if err != nil {
				return
			}

			if !SubsetOf(subset, cond) {
				continue
			}
		}

		var partitionKeys expression.Expressions
		partitionKeys, err = indexPartitionKeys(index, formalizer)
		if err != nil {
			return
		}

		min, sum := SargableFor(pred, keys)
		entry := &indexEntry{
			index, keys, keys[0:min], partitionKeys, min, sum, cond, origCond, nil, false, _PUSHDOWN_NONE}
		all[index] = entry

		if min > 0 {
			sargables[index] = entry
		}

		if isArray {
			arrays[index] = entry
		}
	}

	return sargables, all, arrays, nil
}

func indexPartitionKeys(index datastore.Index,
	formalizer *expression.Formalizer) (partitionKeys expression.Expressions, err error) {

	index3, ok := index.(datastore.Index3)
	if !ok {
		return
	}

	partitionInfo, _ := index3.PartitionKeys()
	if partitionInfo == nil || partitionInfo.Strategy == datastore.NO_PARTITION {
		return
	}

	partitionKeys = partitionInfo.Exprs
	if formalizer == nil {
		return partitionKeys, err
	}

	partitionKeys = partitionKeys.Copy()
	for i, key := range partitionKeys {
		key = key.Copy()

		partitionKeys[i], err = formalizer.Map(key)
		if err != nil {
			return nil, err
		}
	}
	return partitionKeys, err
}

func minimalIndexes(sargables map[datastore.Index]*indexEntry, shortest bool,
	pred expression.Expression) map[datastore.Index]*indexEntry {

	for s, se := range sargables {
		for t, te := range sargables {
			if t == s {
				continue
			}

			if narrowerOrEquivalent(se, te, shortest, pred) {
				delete(sargables, t)
			}
		}
	}

	return sargables
}

/*
Is se narrower or equivalent to te.
*/
func narrowerOrEquivalent(se, te *indexEntry, shortest bool, pred expression.Expression) bool {
	if len(te.sargKeys) > len(se.sargKeys) {
		return false
	}

	if te.cond != nil && (se.cond == nil || !SubsetOf(se.cond, te.cond)) {
		return false
	}

	var fc map[string]value.Value
	var predFc map[string]value.Value
	if se.cond != nil {
		fc = _FILTER_COVERS_POOL.Get()
		defer _FILTER_COVERS_POOL.Put(fc)
		fc = se.cond.FilterCovers(fc)
	}

	if shortest && pred != nil {
		predFc = _FILTER_COVERS_POOL.Get()
		defer _FILTER_COVERS_POOL.Put(predFc)
		predFc = pred.FilterCovers(predFc)
	}

	nfcmatch := 0
outer:
	for _, tk := range te.sargKeys {
		for _, sk := range se.sargKeys {
			if SubsetOf(sk, tk) || sk.DependsOn(tk) {
				continue outer
			}
		}

		if se.cond == nil {
			return false
		}

		/* Count number of matches
		 * Indexkey is part of other index condition as equality predicate
		 * If trying to determine shortest index(For: IntersectScan)
		 *     indexkey is not equality predicate and indexkey is part of other index condition
		 *     (In case of equality predicate keeping IntersectScan might be better)
		 */
		_, condEq := fc[tk.String()]
		_, predEq := predFc[tk.String()]
		if condEq || (shortest && !predEq && se.cond.DependsOn(tk)) {
			nfcmatch++
		} else {
			return false
		}
	}

	if len(te.sargKeys) == nfcmatch {
		return true
	}

	return se.sumKeys > te.sumKeys ||
		(shortest && (len(se.keys) <= len(te.keys)))
}

func (this *builder) sargIndexes(baseKeyspace *baseKeyspace, underHash bool, sargables map[datastore.Index]*indexEntry) error {

	pred := baseKeyspace.dnfPred
	isOrPred := false
	orIsJoin := false
	if !underHash {
		if _, ok := pred.(*expression.Or); ok {
			isOrPred = true
			for _, fl := range baseKeyspace.filters {
				if fl.isJoin() {
					orIsJoin = true
					break
				}
			}
		}
	}

	for _, se := range sargables {
		var spans SargSpans
		var exactSpans bool
		var err error

		if isOrPred {
			spans, exactSpans, err = SargFor(baseKeyspace.dnfPred, se.keys, se.minKeys, orIsJoin, baseKeyspace.name)
		} else {
			spans, exactSpans, err = SargForFilters(baseKeyspace.filters, se.keys, se.minKeys, underHash, baseKeyspace.name)
		}
		if err != nil || spans.Size() == 0 {
			logging.Errorp("Sargable index not sarged", logging.Pair{"pred", fmt.Sprintf("<ud>%v</ud>", pred)},
				logging.Pair{"sarg_keys", fmt.Sprintf("<ud>%v</ud>", se.sargKeys)}, logging.Pair{"error", err})

			return errors.NewPlanError(nil, fmt.Sprintf("Sargable index not sarged; pred=%v, sarg_keys=%v, error=%v",
				pred.String(), se.sargKeys.String(), err))
		}

		se.spans = spans
		if exactSpans && !useIndex2API(se.index, this.indexApiVersion) {
			exactSpans = spans.ExactSpan1(len(se.keys))
		}
		se.exactSpans = exactSpans
	}

	return nil
}

func indexHasArrayIndexKey(index datastore.Index) bool {
	for _, sk := range index.RangeKey() {
		if isArray, _ := sk.IsArrayIndexKey(); isArray {
			return true
		}
	}
	return false
}

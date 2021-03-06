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
	"github.com/couchbase/query/algebra"
	"github.com/couchbase/query/datastore"
	"github.com/couchbase/query/expression"
	"github.com/couchbase/query/expression/parser"
	"github.com/couchbase/query/plan"
	"github.com/couchbase/query/util"
	"github.com/couchbase/query/value"
)

func (this *builder) buildCoveringScan(indexes map[datastore.Index]*indexEntry,
	node *algebra.KeyspaceTerm, baseKeyspace *baseKeyspace,
	id expression.Expression) (plan.SecondaryScan, int, error) {

	if this.cover == nil {
		return nil, 0, nil
	}

	alias := node.Alias()
	exprs := this.cover.Expressions()
	pred := baseKeyspace.dnfPred
	origPred := baseKeyspace.origPred

	arrays := _ARRAY_POOL.Get()
	defer _ARRAY_POOL.Put(arrays)

	covering := _COVERING_POOL.Get()
	defer _COVERING_POOL.Put(covering)

	// Remember filter covers
	fc := make(map[datastore.Index]map[*expression.Cover]value.Value, len(indexes))

outer:
	for index, entry := range indexes {
		hasArrayKey := indexHasArrayIndexKey(index)
		if hasArrayKey && (len(arrays) < len(covering)) {
			continue
		}

		// Sarg to set spans
		err := this.sargIndexes(baseKeyspace, node.IsUnderHash(), map[datastore.Index]*indexEntry{index: entry})
		if err != nil {
			return nil, 0, err
		}

		keys := entry.keys

		// Matches execution.spanScan.RunOnce()
		if !index.IsPrimary() {
			keys = append(keys, id)
		}

		// Include filter covers
		coveringExprs, filterCovers, err := indexCoverExpressions(entry, keys, pred, origPred)
		if err != nil {
			return nil, 0, err
		}

		// Skip non-covering index
		for _, expr := range exprs {
			if !expression.IsCovered(expr, alias, coveringExprs) {
				continue outer
			}
		}

		if hasArrayKey {
			arrays[index] = true
		}

		covering[index] = true
		fc[index] = filterCovers
		entry.pushDownProperty = this.indexPushDownProperty(entry, keys, nil, pred, alias, false, covering[index])
	}

	// No covering index available
	if len(covering) == 0 {
		return nil, 0, nil
	}

	// Avoid array indexes if possible
	if len(arrays) < len(covering) {
		for a, _ := range arrays {
			delete(covering, a)
		}
	}

	// Keep indexes with max sumKeys
	sumKeys := 0
	for c, _ := range covering {
		if max := indexes[c].sumKeys; max > sumKeys {
			sumKeys = max
		}
	}

	for c, _ := range covering {
		if indexes[c].sumKeys < sumKeys {
			delete(covering, c)
		}
	}

	// Use shortest remaining index
	var index datastore.Index
	minLen := 0
	for c, _ := range covering {
		cLen := len(c.RangeKey())
		if index == nil ||
			indexes[c].PushDownProperty() > indexes[index].PushDownProperty() ||
			cLen < minLen || (cLen == minLen && c.Condition() != nil) {
			index = c
			minLen = cLen
		}
	}

	entry := indexes[index]
	sargLength := len(entry.sargKeys)
	keys := entry.keys

	// Matches execution.spanScan.RunOnce()
	if !index.IsPrimary() {
		keys = append(keys, id)
	}

	// Include covering expression from index WHERE clause
	filterCovers := fc[index]

	// Include covering expression from index keys
	covers := make(expression.Covers, 0, len(keys))
	for _, key := range keys {
		covers = append(covers, expression.NewCover(key))
	}

	arrayIndex := arrays[index]
	duplicates := entry.spans.CanHaveDuplicates(index, this.indexApiVersion, pred.MayOverlapSpans(), false)
	indexProjection := this.buildIndexProjection(entry, exprs, id, index.IsPrimary() || arrayIndex || duplicates)

	// Check and reset pagination pushdows
	indexKeyOrders := this.checkResetPaginations(entry, keys)

	// Build old Aggregates on Index2 only
	scan := this.buildCoveringPushdDownIndexScan2(entry, node, pred, indexProjection,
		!arrayIndex, false, covers, filterCovers)
	if scan != nil {
		return scan, sargLength, nil
	}

	// Aggregates check and reset
	var indexGroupAggs *plan.IndexGroupAggregates
	if !entry.IsPushDownProperty(_PUSHDOWN_GROUPAGGS) {
		this.resetIndexGroupAggs()
	}

	// build plan for aggregates
	indexGroupAggs, indexProjection = this.buildIndexGroupAggs(entry, keys, false, indexProjection)
	projDistinct := entry.IsPushDownProperty(_PUSHDOWN_DISTINCT)

	// build plan for IndexScan
	scan = entry.spans.CreateScan(index, node, this.indexApiVersion, false, projDistinct, pred.MayOverlapSpans(), false,
		this.offset, this.limit, indexProjection, indexKeyOrders, indexGroupAggs, covers, filterCovers)
	if scan != nil {
		this.coveringScans = append(this.coveringScans, scan)
	}

	return scan, sargLength, nil
}

func (this *builder) checkResetPaginations(entry *indexEntry,
	keys expression.Expressions) (indexKeyOrders plan.IndexKeyOrders) {

	// check order pushdown and reset
	if this.order != nil {
		if entry.IsPushDownProperty(_PUSHDOWN_ORDER) {
			_, indexKeyOrders = this.useIndexOrder(entry, keys)
			this.maxParallelism = 1
		} else {
			this.resetOrderOffsetLimit()
		}
	}

	// check offset push down and convert limit = limit + offset
	if this.offset != nil && !entry.IsPushDownProperty(_PUSHDOWN_OFFSET) {
		this.limit = offsetPlusLimit(this.offset, this.limit)
		this.resetOffset()
	}

	// check limit and reset
	if this.limit != nil && !entry.IsPushDownProperty(_PUSHDOWN_LIMIT) {
		this.resetLimit()
	}
	return
}

func (this *builder) buildCoveringPushdDownIndexScan2(entry *indexEntry, node *algebra.KeyspaceTerm,
	pred expression.Expression, indexProjection *plan.IndexProjection, countPush, array bool,
	covers expression.Covers, filterCovers map[*expression.Cover]value.Value) plan.SecondaryScan {

	// Aggregates supported pre-Index3
	if (useIndex3API(entry.index, this.indexApiVersion) &&
		util.IsFeatureEnabled(this.featureControls, util.N1QL_GROUPAGG_PUSHDOWN)) || !this.oldAggregates ||
		!entry.IsPushDownProperty(_PUSHDOWN_GROUPAGGS) {
		return nil
	}

	defer func() { this.resetIndexGroupAggs() }()

	var indexKeyOrders plan.IndexKeyOrders

	for _, ag := range this.aggs {
		switch agg := ag.(type) {
		case *algebra.Count, *algebra.CountDistinct:
			if !countPush {
				return nil
			}

			distinct := agg.Distinct()
			op := agg.Operand()
			if !distinct || op.Value() == nil {
				scan := this.buildIndexCountScan(node, entry, pred, distinct, covers, filterCovers)
				this.countScan = scan
				return scan
			}

		case *algebra.Min, *algebra.Max:
			indexKeyOrders = make(plan.IndexKeyOrders, 1)
			if _, ok := agg.(*algebra.Min); ok {
				indexKeyOrders[0] = plan.NewIndexKeyOrders(0, false)
			} else {
				indexKeyOrders[0] = plan.NewIndexKeyOrders(0, true)
			}
		default:
			return nil
		}
	}

	this.maxParallelism = 1
	scan := entry.spans.CreateScan(entry.index, node, this.indexApiVersion, false, false, pred.MayOverlapSpans(),
		array, nil, expression.ONE_EXPR, indexProjection, indexKeyOrders, nil, covers, filterCovers)
	if scan != nil {
		this.coveringScans = append(this.coveringScans, scan)
	}

	return scan
}

func mapFilterCovers(fc map[string]value.Value) (map[*expression.Cover]value.Value, error) {
	if len(fc) == 0 {
		return nil, nil
	}

	rv := make(map[*expression.Cover]value.Value, len(fc))
	for s, v := range fc {
		expr, err := parser.Parse(s)
		if err != nil {
			return nil, err
		}

		c := expression.NewCover(expr)
		rv[c] = v
	}

	return rv, nil
}

func indexCoverExpressions(entry *indexEntry, keys expression.Expressions, pred, origPred expression.Expression) (
	expression.Expressions, map[*expression.Cover]value.Value, error) {

	var filterCovers map[*expression.Cover]value.Value
	exprs := make(expression.Expressions, 0, len(keys))
	exprs = append(exprs, keys...)
	if entry.cond != nil {
		var err error
		fc := _FILTER_COVERS_POOL.Get()
		defer _FILTER_COVERS_POOL.Put(fc)
		fc = entry.cond.FilterCovers(fc)
		fc = entry.origCond.FilterCovers(fc)
		filterCovers, err = mapFilterCovers(fc)
		if err != nil {
			return nil, nil, err
		}
	}

	// Allow array indexes to cover ANY predicates
	if pred != nil && entry.exactSpans && indexHasArrayIndexKey(entry.index) {
		sargKeysHasArray := false
		for _, sk := range entry.sargKeys {
			if sargKeysHasArray, _ = sk.IsArrayIndexKey(); sargKeysHasArray {
				break
			}
		}

		if _, ok := entry.spans.(*IntersectSpans); !ok && sargKeysHasArray {
			covers, err := CoversFor(pred, origPred, keys)
			if err != nil {
				return nil, nil, err
			}

			if len(covers) > 0 {
				if len(filterCovers) == 0 {
					filterCovers = covers
				} else {
					for c, v := range covers {
						if _, ok := filterCovers[c]; !ok {
							filterCovers[c] = v
						}
					}
				}
			}
		}
	}

	if len(filterCovers) > 0 {
		exprs = make(expression.Expressions, len(keys), len(keys)+len(filterCovers))
		copy(exprs, keys)

		for c, _ := range filterCovers {
			exprs = append(exprs, c.Covered())
		}
	}

	return exprs, filterCovers, nil
}

var _ARRAY_POOL = datastore.NewIndexBoolPool(64)
var _COVERING_POOL = datastore.NewIndexBoolPool(64)
var _FILTER_COVERS_POOL = value.NewStringValuePool(32)
var _STRING_BOOL_POOL = util.NewStringBoolPool(1024)

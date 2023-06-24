package gplus

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type Condition struct {
	Group       string
	ColumnName  string
	Op          string
	ColumnValue any
}

var columnTypeCache sync.Map

var operators = []string{"!~<=", "!~>=", "~<=", "~>=", "!?=", "!^=", "!~=", "?=", "^=", "~=", "!=", ">=", "<=", "=", ">", "<"}
var builders = map[string]func(query *QueryCond[any], name string, value any){
	"!~<=": notLikeLeft,
	"!~>=": notLikeRight,
	"~<=":  LikeLeft,
	"~>=":  LikeRight,
	"!?=":  notIn,
	"!^=":  notBetween,
	"!~=":  notLike,
	"?=":   in,
	"^=":   between,
	"~=":   like,
	"!=":   ne,
	">=":   ge,
	"<=":   le,
	"=":    eq,
	">":    gt,
	"<":    lt,
}

func BuildQuery[T any](queryParams url.Values) *QueryCond[T] {

	columnConditionMap, conditionMap, gcond := parseParams(queryParams)

	queryCondMap := buildQueryCondMap[T](columnConditionMap, conditionMap)

	// 如果没有分组条件，直接返回默认的查询条件
	if len(gcond) == 0 {
		return queryCondMap["default"]
	}

	return buildGroupQuery[T](gcond, queryCondMap)
}

func parseParams(queryParams url.Values) (map[string][]*Condition, map[string]any, string) {
	var gcond string
	var columnConditionMap = make(map[string][]*Condition)
	var conditionMap = make(map[string]any)
	for key, values := range queryParams {
		if key == "q" {
			columnConditionMap = buildConditionMap(values)
		} else if key == "page" {
			if len(values) > 0 {
				conditionMap["page"] = values[len(values)-1]
			}
		} else if key == "size" {
			if len(values) > 0 {
				conditionMap["size"] = values[len(values)-1]
			}
		} else if key == "isTotal" {
			if len(values) > 0 {
				conditionMap["isTotal"] = values[len(values)-1]
			}
		} else if key == "sort" {
			if len(values) > 0 {
				conditionMap["sort"] = values[len(values)-1]
			}
		} else if key == "select" {
			if len(values) > 0 {
				conditionMap["select"] = values[len(values)-1]
			}
		} else if key == "omit" {
			if len(values) > 0 {
				conditionMap["omit"] = values[len(values)-1]
			}
		} else if key == "gcond" {
			gcond = values[0]
		}
	}
	return columnConditionMap, conditionMap, gcond
}

func buildConditionMap(values []string) map[string][]*Condition {
	var maps = make(map[string][]*Condition)
	for _, value := range values {
		currentOperator := getCurrentOp(value)
		params := strings.SplitN(value, currentOperator, 2)
		if len(params) == 2 {
			condition := &Condition{}
			groups := strings.Split(params[0], ".")
			var groupName string
			var columnName string
			// 如果不包含组，默认分为同一个组
			if len(groups) == 1 {
				groupName = "default"
				columnName = groups[0]
			} else if len(groups) == 2 {
				groupName = groups[0]
				columnName = groups[1]
			}
			condition.Group = groupName
			condition.ColumnName = columnName
			condition.Op = currentOperator
			condition.ColumnValue = params[1]
			conditions, ok := maps[groupName]
			if ok {
				conditions = append(conditions, condition)
			} else {
				conditions = []*Condition{condition}
			}
			maps[groupName] = conditions
		}
	}
	return maps
}

func getCurrentOp(value string) string {
	var currentOperator string
	for _, op := range operators {
		if strings.Contains(value, op) {
			currentOperator = op
			break
		}
	}
	return currentOperator
}

func buildQueryCondMap[T any](columnConditionMap map[string][]*Condition, conditionMap map[string]any) map[string]*QueryCond[T] {
	var queryMaps = make(map[string]*QueryCond[T])
	columnTypeMap := getColumnTypeMap[T]()
	for key, conditions := range columnConditionMap {
		query := &QueryCond[any]{}
		query.columnTypeMap = columnTypeMap
		for _, condition := range conditions {
			name := condition.ColumnName
			op := condition.Op
			value := condition.ColumnValue
			builders[op](query, name, value)
		}
		newQuery, _ := NewQuery[T]()
		newQuery.queryExpressions = append(newQuery.queryExpressions, query.queryExpressions...)
		queryMaps[key] = newQuery
	}
	return queryMaps
}

func buildGroupQuery[T any](gcond string, queryMaps map[string]*QueryCond[T]) *QueryCond[T] {
	query, _ := NewQuery[T]()
	var tempQuerys []*QueryCond[T]
	tempQuerys = append(tempQuerys, query)
	for i, char := range gcond {
		str := string(char)
		tempQuery := tempQuerys[len(tempQuerys)-1]
		// 如果是 左括号 开头，则代表需要嵌套查询
		if str == "(" && i != len(gcond)-1 {
			if i != 0 && string(gcond[i-1]) == "|" {
				tempQuery.Or(func(q *QueryCond[T]) {
					paramQuery, isOk := queryMaps[string(gcond[i+1])]
					if isOk {
						q.queryExpressions = append(q.queryExpressions, paramQuery.queryExpressions...)
						tempQuerys = append(tempQuerys, q)
					}
				})
				continue
			} else {
				tempQuery.And(func(q *QueryCond[T]) {
					paramQuery, isOk := queryMaps[string(gcond[i+1])]
					if isOk {
						q.queryExpressions = append(q.queryExpressions, paramQuery.queryExpressions...)
						tempQuerys = append(tempQuerys, q)
					}
				})
			}
			continue
		}

		// 如果当前为 | ,而且不是最后一个字符，而且下一个字符不是 ( ,则为 or
		if str == "|" && i != len(gcond)-1 {
			paramQuery, isOk := queryMaps[string(gcond[i+1])]
			if isOk {
				tempQuery.Or().queryExpressions = append(tempQuery.queryExpressions, paramQuery.queryExpressions...)
				tempQuery.last = paramQuery.queryExpressions[len(paramQuery.queryExpressions)-1]
			}
			continue
		}

		if str == "*" && i != len(gcond)-1 {
			paramQuery, isOk := queryMaps[string(gcond[i+1])]
			if isOk {
				tempQuery.And()
				tempQuery.queryExpressions = append(tempQuery.queryExpressions, paramQuery.queryExpressions...)
				tempQuery.last = paramQuery.queryExpressions[len(paramQuery.queryExpressions)-1]
			}
			continue
		}

		if str == ")" {
			// 删除最后一个query对象
			tempQuerys = tempQuerys[:len(tempQuerys)-1]
		}
	}
	return query
}

func getColumnTypeMap[T any]() map[string]reflect.Type {
	modelTypeStr := reflect.TypeOf((*T)(nil)).Elem().String()
	if model, ok := columnTypeCache.Load(modelTypeStr); ok {
		if columnNameMap, isOk := model.(map[string]reflect.Type); isOk {
			return columnNameMap
		}
	}

	var columnNameMap = make(map[string]reflect.Type)
	typeOf := reflect.TypeOf((*T)(nil)).Elem()
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		columnName := parseColumnName(field)
		columnNameMap[columnName] = field.Type
	}
	columnTypeCache.Store(modelTypeStr, columnNameMap)
	return columnNameMap
}

func notLikeLeft(query *QueryCond[any], name string, value any) {
	query.NotLikeLeft(name, convert(query.columnTypeMap, name, value))
}

func notLikeRight(query *QueryCond[any], name string, value any) {
	query.NotLikeRight(name, convert(query.columnTypeMap, name, value))
}

func LikeLeft(query *QueryCond[any], name string, value any) {
	query.LikeLeft(name, convert(query.columnTypeMap, name, value))
}

func LikeRight(query *QueryCond[any], name string, value any) {
	query.LikeRight(name, convert(query.columnTypeMap, name, value))
}

func notIn(query *QueryCond[any], name string, value any) {
	values := strings.Split(fmt.Sprintf("%s", value), ",")
	var queryValues []any
	for _, v := range values {
		queryValues = append(queryValues, convert(query.columnTypeMap, name, v))
	}
	query.NotIn(name, queryValues)
}

func notBetween(query *QueryCond[any], name string, value any) {
	values := strings.Split(fmt.Sprintf("%s", value), ",")
	if len(values) == 2 {
		query.NotBetween(name, convert(query.columnTypeMap, name, values[0]), convert(query.columnTypeMap, name, values[1]))
	}
}

func notLike(query *QueryCond[any], name string, value any) {
	query.NotLike(name, convert(query.columnTypeMap, name, value))
}

func in(query *QueryCond[any], name string, value any) {
	values := strings.Split(fmt.Sprintf("%s", value), ",")
	var queryValues []any
	for _, v := range values {
		queryValues = append(queryValues, convert(query.columnTypeMap, name, v))
	}
	query.In(name, queryValues)
}

func between(query *QueryCond[any], name string, value any) {
	values := strings.Split(fmt.Sprintf("%s", value), ",")
	if len(values) == 2 {
		query.Between(name, convert(query.columnTypeMap, name, values[0]), convert(query.columnTypeMap, name, values[1]))
	}
}

func like(query *QueryCond[any], name string, value any) {
	query.Like(name, convert(query.columnTypeMap, name, value))
}

func ne(query *QueryCond[any], name string, value any) {
	if strings.ToLower(fmt.Sprintf("%s", value)) == "null" {
		query.IsNotNull(name)
	} else {
		query.Ne(name, convert(query.columnTypeMap, name, value))
	}
}

func ge(query *QueryCond[any], name string, value any) {
	query.Ge(name, convert(query.columnTypeMap, name, value))
}

func le(query *QueryCond[any], name string, value any) {
	query.Le(name, convert(query.columnTypeMap, name, value))
}

func eq(query *QueryCond[any], name string, value any) {
	if strings.ToLower(fmt.Sprintf("%s", value)) == "null" {
		query.IsNull(name)
	} else {
		query.Eq(name, convert(query.columnTypeMap, name, value))
	}
}

func gt(query *QueryCond[any], name string, value any) {
	query.Gt(name, convert(query.columnTypeMap, name, value))
}

func lt(query *QueryCond[any], name string, value any) {
	query.Lt(name, convert(query.columnTypeMap, name, value))
}

func convert(columnTypeMap map[string]reflect.Type, name string, value any) any {
	columnType, ok := columnTypeMap[name]
	if ok {
		switch columnType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			atoi, err := strconv.Atoi(fmt.Sprintf("%s", value))
			if err == nil {
				value = atoi
			}
		}
	}
	return value
}

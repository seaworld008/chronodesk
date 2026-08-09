package database

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type webhookCheckTokenKind uint8

const (
	webhookCheckTokenIdentifier webhookCheckTokenKind = iota
	webhookCheckTokenString
	webhookCheckTokenInteger
	webhookCheckTokenLeftParen
	webhookCheckTokenRightParen
	webhookCheckTokenLeftBracket
	webhookCheckTokenRightBracket
	webhookCheckTokenComma
	webhookCheckTokenEqual
	webhookCheckTokenNotEqual
	webhookCheckTokenGreater
	webhookCheckTokenAnd
	webhookCheckTokenOr
	webhookCheckTokenIs
	webhookCheckTokenNot
	webhookCheckTokenNull
	webhookCheckTokenIn
	webhookCheckTokenAny
	webhookCheckTokenArray
	webhookCheckTokenCast
)

type webhookCheckToken struct {
	kind   webhookCheckTokenKind
	text   string
	quoted bool
}

type webhookCheckNodeKind uint8

const (
	webhookCheckNodeAnd webhookCheckNodeKind = iota
	webhookCheckNodeOr
	webhookCheckNodeCompare
	webhookCheckNodeIsNull
	webhookCheckNodeIsNotNull
	webhookCheckNodeClosedSet
)

type webhookCheckValueKind uint8

const (
	webhookCheckIdentifier webhookCheckValueKind = iota
	webhookCheckString
	webhookCheckInteger
)

type webhookCheckValue struct {
	kind   webhookCheckValueKind
	text   string
	quoted bool
}

type webhookCheckNode struct {
	kind     webhookCheckNodeKind
	operator string
	left     webhookCheckValue
	right    webhookCheckValue
	values   []webhookCheckValue
	children []*webhookCheckNode
}

type webhookCheckParser struct {
	tokens []webhookCheckToken
	index  int
}

func canonicalWebhookConstraintDefinition(
	definition string,
) (string, error) {
	expression := strings.TrimSpace(definition)
	if len(expression) >= len("check") &&
		strings.EqualFold(expression[:len("check")], "check") {
		if len(expression) > len("check") {
			next := rune(expression[len("check")])
			if !unicode.IsSpace(next) && next != '(' {
				return "", fmt.Errorf(
					"unsupported CHECK prefix in %q",
					definition,
				)
			}
		}
		expression = strings.TrimSpace(expression[len("check"):])
	}
	tokens, err := lexWebhookCheckExpression(expression)
	if err != nil {
		return "", err
	}
	parser := webhookCheckParser{tokens: tokens}
	node, err := parser.parseOr()
	if err != nil {
		return "", err
	}
	if parser.index != len(tokens) {
		return "", fmt.Errorf(
			"unexpected CHECK token %q",
			tokens[parser.index].text,
		)
	}
	node = normalizeWebhookCheckNode(node)
	return serializeWebhookCheckNode(node), nil
}

func lexWebhookCheckExpression(
	expression string,
) ([]webhookCheckToken, error) {
	tokens := make([]webhookCheckToken, 0, 32)
	for index := 0; index < len(expression); {
		current := expression[index]
		if unicode.IsSpace(rune(current)) {
			index++
			continue
		}
		if current == '-' && index+1 < len(expression) &&
			expression[index+1] == '-' ||
			current == '/' && index+1 < len(expression) &&
				expression[index+1] == '*' {
			return nil, fmt.Errorf("comments are forbidden in CHECK expressions")
		}
		switch current {
		case '(':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenLeftParen,
				text: "(",
			})
			index++
		case ')':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenRightParen,
				text: ")",
			})
			index++
		case '[':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenLeftBracket,
				text: "[",
			})
			index++
		case ']':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenRightBracket,
				text: "]",
			})
			index++
		case ',':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenComma,
				text: ",",
			})
			index++
		case '=':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenEqual,
				text: "=",
			})
			index++
		case '>':
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenGreater,
				text: ">",
			})
			index++
		case '<':
			if index+1 >= len(expression) ||
				expression[index+1] != '>' {
				return nil, fmt.Errorf(
					"unsupported CHECK operator at byte %d",
					index,
				)
			}
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenNotEqual,
				text: "<>",
			})
			index += 2
		case ':':
			if index+1 >= len(expression) ||
				expression[index+1] != ':' {
				return nil, fmt.Errorf(
					"unsupported CHECK token at byte %d",
					index,
				)
			}
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenCast,
				text: "::",
			})
			index += 2
		case '\'':
			value, next, err := scanWebhookCheckQuoted(
				expression,
				index,
				'\'',
			)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, webhookCheckToken{
				kind: webhookCheckTokenString,
				text: value,
			})
			index = next
		case '"', '`':
			value, next, err := scanWebhookCheckQuoted(
				expression,
				index,
				current,
			)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, webhookCheckToken{
				kind:   webhookCheckTokenIdentifier,
				text:   value,
				quoted: true,
			})
			index = next
		default:
			switch {
			case current >= '0' && current <= '9':
				start := index
				for index < len(expression) &&
					expression[index] >= '0' &&
					expression[index] <= '9' {
					index++
				}
				tokens = append(tokens, webhookCheckToken{
					kind: webhookCheckTokenInteger,
					text: expression[start:index],
				})
			case isWebhookCheckIdentifierStart(current):
				start := index
				index++
				for index < len(expression) &&
					isWebhookCheckIdentifierPart(expression[index]) {
					index++
				}
				word := strings.ToLower(expression[start:index])
				tokens = append(tokens, webhookCheckKeywordToken(word))
			default:
				return nil, fmt.Errorf(
					"unsupported CHECK token %q at byte %d",
					current,
					index,
				)
			}
		}
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("CHECK expression is empty")
	}
	return tokens, nil
}

func scanWebhookCheckQuoted(
	value string,
	start int,
	quote byte,
) (string, int, error) {
	var builder strings.Builder
	for index := start + 1; index < len(value); index++ {
		if value[index] != quote {
			builder.WriteByte(value[index])
			continue
		}
		if index+1 < len(value) && value[index+1] == quote {
			builder.WriteByte(quote)
			index++
			continue
		}
		return builder.String(), index + 1, nil
	}
	return "", 0, fmt.Errorf("unterminated quoted CHECK token")
}

func webhookCheckKeywordToken(word string) webhookCheckToken {
	kinds := map[string]webhookCheckTokenKind{
		"and":   webhookCheckTokenAnd,
		"or":    webhookCheckTokenOr,
		"is":    webhookCheckTokenIs,
		"not":   webhookCheckTokenNot,
		"null":  webhookCheckTokenNull,
		"in":    webhookCheckTokenIn,
		"any":   webhookCheckTokenAny,
		"array": webhookCheckTokenArray,
	}
	if kind, exists := kinds[word]; exists {
		return webhookCheckToken{kind: kind, text: word}
	}
	return webhookCheckToken{
		kind: webhookCheckTokenIdentifier,
		text: word,
	}
}

func isWebhookCheckIdentifierStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func isWebhookCheckIdentifierPart(value byte) bool {
	return isWebhookCheckIdentifierStart(value) ||
		value >= '0' && value <= '9'
}

func (parser *webhookCheckParser) parseOr() (*webhookCheckNode, error) {
	left, err := parser.parseAnd()
	if err != nil {
		return nil, err
	}
	children := []*webhookCheckNode{left}
	for parser.match(webhookCheckTokenOr) {
		right, err := parser.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &webhookCheckNode{
		kind:     webhookCheckNodeOr,
		children: children,
	}, nil
}

func (parser *webhookCheckParser) parseAnd() (*webhookCheckNode, error) {
	left, err := parser.parsePrimary()
	if err != nil {
		return nil, err
	}
	children := []*webhookCheckNode{left}
	for parser.match(webhookCheckTokenAnd) {
		right, err := parser.parsePrimary()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &webhookCheckNode{
		kind:     webhookCheckNodeAnd,
		children: children,
	}, nil
}

func (parser *webhookCheckParser) parsePrimary() (*webhookCheckNode, error) {
	saved := parser.index
	if predicate, err := parser.parsePredicate(); err == nil {
		return predicate, nil
	}
	parser.index = saved
	if !parser.match(webhookCheckTokenLeftParen) {
		return nil, parser.unexpected("predicate or parenthesized expression")
	}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if !parser.match(webhookCheckTokenRightParen) {
		return nil, parser.unexpected(")")
	}
	return node, nil
}

func (parser *webhookCheckParser) parsePredicate() (*webhookCheckNode, error) {
	left, err := parser.parseValue()
	if err != nil {
		return nil, err
	}
	if parser.match(webhookCheckTokenIs) {
		if parser.match(webhookCheckTokenNot) {
			if !parser.match(webhookCheckTokenNull) {
				return nil, parser.unexpected("NULL")
			}
			return &webhookCheckNode{
				kind: webhookCheckNodeIsNotNull,
				left: left,
			}, nil
		}
		if !parser.match(webhookCheckTokenNull) {
			return nil, parser.unexpected("NULL")
		}
		return &webhookCheckNode{
			kind: webhookCheckNodeIsNull,
			left: left,
		}, nil
	}
	if parser.match(webhookCheckTokenIn) {
		values, err := parser.parseClosedSetValues(
			webhookCheckTokenLeftParen,
			webhookCheckTokenRightParen,
		)
		if err != nil {
			return nil, err
		}
		return &webhookCheckNode{
			kind:   webhookCheckNodeClosedSet,
			left:   left,
			values: values,
		}, nil
	}
	operator := ""
	switch {
	case parser.match(webhookCheckTokenEqual):
		operator = "="
	case parser.match(webhookCheckTokenNotEqual):
		operator = "<>"
	case parser.match(webhookCheckTokenGreater):
		operator = ">"
	default:
		return nil, parser.unexpected("comparison operator")
	}
	if operator == "=" && parser.match(webhookCheckTokenAny) {
		values, err := parser.parseAnyArray()
		if err != nil {
			return nil, err
		}
		return &webhookCheckNode{
			kind:   webhookCheckNodeClosedSet,
			left:   left,
			values: values,
		}, nil
	}
	right, err := parser.parseValue()
	if err != nil {
		return nil, err
	}
	return &webhookCheckNode{
		kind:     webhookCheckNodeCompare,
		operator: operator,
		left:     left,
		right:    right,
	}, nil
}

func (parser *webhookCheckParser) parseValue() (webhookCheckValue, error) {
	var value webhookCheckValue
	if parser.match(webhookCheckTokenLeftParen) {
		nested, err := parser.parseValue()
		if err != nil {
			return value, err
		}
		if !parser.match(webhookCheckTokenRightParen) {
			return value, parser.unexpected(")")
		}
		value = nested
	} else {
		if parser.index >= len(parser.tokens) {
			return value, parser.unexpected("identifier or literal")
		}
		token := parser.tokens[parser.index]
		parser.index++
		switch token.kind {
		case webhookCheckTokenIdentifier:
			value = webhookCheckValue{
				kind:   webhookCheckIdentifier,
				text:   token.text,
				quoted: token.quoted,
			}
		case webhookCheckTokenString:
			value = webhookCheckValue{
				kind: webhookCheckString,
				text: token.text,
			}
		case webhookCheckTokenInteger:
			if _, err := strconv.ParseUint(token.text, 10, 64); err != nil {
				return value, fmt.Errorf(
					"invalid CHECK integer %q",
					token.text,
				)
			}
			value = webhookCheckValue{
				kind: webhookCheckInteger,
				text: token.text,
			}
		default:
			parser.index--
			return value, parser.unexpected("identifier or literal")
		}
	}
	if err := parser.consumeWhitelistedCasts(); err != nil {
		return value, err
	}
	return value, nil
}

func (parser *webhookCheckParser) consumeWhitelistedCasts() error {
	for parser.match(webhookCheckTokenCast) {
		if parser.index >= len(parser.tokens) ||
			parser.tokens[parser.index].kind !=
				webhookCheckTokenIdentifier {
			return parser.unexpected("whitelisted cast")
		}
		castName := parser.tokens[parser.index].text
		parser.index++
		switch castName {
		case "text":
		case "character":
			if parser.index >= len(parser.tokens) ||
				parser.tokens[parser.index].kind !=
					webhookCheckTokenIdentifier ||
				parser.tokens[parser.index].text != "varying" {
				return fmt.Errorf(
					"unsupported CHECK cast character without varying",
				)
			}
			parser.index++
		default:
			return fmt.Errorf(
				"unsupported CHECK cast %q",
				castName,
			)
		}
		if parser.match(webhookCheckTokenLeftBracket) {
			if !parser.match(webhookCheckTokenRightBracket) {
				return parser.unexpected("]")
			}
		}
	}
	return nil
}

func (parser *webhookCheckParser) parseClosedSetValues(
	open webhookCheckTokenKind,
	close webhookCheckTokenKind,
) ([]webhookCheckValue, error) {
	if !parser.match(open) {
		return nil, parser.unexpected("closed-set opening delimiter")
	}
	values := make([]webhookCheckValue, 0)
	for {
		value, err := parser.parseValue()
		if err != nil {
			return nil, err
		}
		if value.kind == webhookCheckIdentifier {
			return nil, fmt.Errorf(
				"closed CHECK vocabulary cannot contain identifiers",
			)
		}
		values = append(values, value)
		if parser.match(close) {
			break
		}
		if !parser.match(webhookCheckTokenComma) {
			return nil, parser.unexpected(",")
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("closed CHECK vocabulary is empty")
	}
	return values, nil
}

func (parser *webhookCheckParser) parseAnyArray() ([]webhookCheckValue, error) {
	if !parser.match(webhookCheckTokenLeftParen) {
		return nil, parser.unexpected("(ARRAY")
	}
	groupedArray := parser.match(webhookCheckTokenLeftParen)
	if !parser.match(webhookCheckTokenArray) {
		return nil, parser.unexpected("ARRAY")
	}
	values, err := parser.parseClosedSetValues(
		webhookCheckTokenLeftBracket,
		webhookCheckTokenRightBracket,
	)
	if err != nil {
		return nil, err
	}
	if groupedArray && !parser.match(webhookCheckTokenRightParen) {
		return nil, parser.unexpected(")")
	}
	if err := parser.consumeWhitelistedCasts(); err != nil {
		return nil, err
	}
	if !parser.match(webhookCheckTokenRightParen) {
		return nil, parser.unexpected(")")
	}
	return values, nil
}

func (parser *webhookCheckParser) match(kind webhookCheckTokenKind) bool {
	if parser.index >= len(parser.tokens) ||
		parser.tokens[parser.index].kind != kind {
		return false
	}
	parser.index++
	return true
}

func (parser *webhookCheckParser) unexpected(expected string) error {
	if parser.index >= len(parser.tokens) {
		return fmt.Errorf("expected %s at end of CHECK expression", expected)
	}
	return fmt.Errorf(
		"expected %s, got %q",
		expected,
		parser.tokens[parser.index].text,
	)
}

func normalizeWebhookCheckNode(
	node *webhookCheckNode,
) *webhookCheckNode {
	if node == nil {
		return nil
	}
	switch node.kind {
	case webhookCheckNodeAnd, webhookCheckNodeOr:
		children := make([]*webhookCheckNode, 0, len(node.children))
		for _, child := range node.children {
			child = normalizeWebhookCheckNode(child)
			if child.kind == node.kind {
				children = append(children, child.children...)
				continue
			}
			children = append(children, child)
		}
		node.children = children
	case webhookCheckNodeClosedSet:
		sort.Slice(node.values, func(left, right int) bool {
			return serializeWebhookCheckValue(node.values[left]) <
				serializeWebhookCheckValue(node.values[right])
		})
	}
	return node
}

func serializeWebhookCheckNode(node *webhookCheckNode) string {
	switch node.kind {
	case webhookCheckNodeAnd:
		return serializeWebhookCheckChildren("and", node.children)
	case webhookCheckNodeOr:
		return serializeWebhookCheckChildren("or", node.children)
	case webhookCheckNodeCompare:
		return "cmp(" + node.operator + "," +
			serializeWebhookCheckValue(node.left) + "," +
			serializeWebhookCheckValue(node.right) + ")"
	case webhookCheckNodeIsNull:
		return "isnull(" + serializeWebhookCheckValue(node.left) + ")"
	case webhookCheckNodeIsNotNull:
		return "isnotnull(" + serializeWebhookCheckValue(node.left) + ")"
	case webhookCheckNodeClosedSet:
		values := make([]string, 0, len(node.values))
		for _, value := range node.values {
			values = append(values, serializeWebhookCheckValue(value))
		}
		return "in(" + serializeWebhookCheckValue(node.left) + "," +
			strings.Join(values, ",") + ")"
	default:
		return "invalid"
	}
}

func serializeWebhookCheckChildren(
	operator string,
	children []*webhookCheckNode,
) string {
	values := make([]string, 0, len(children))
	for _, child := range children {
		values = append(values, serializeWebhookCheckNode(child))
	}
	return operator + "(" + strings.Join(values, ",") + ")"
}

func serializeWebhookCheckValue(value webhookCheckValue) string {
	switch value.kind {
	case webhookCheckIdentifier:
		if value.quoted && value.text != strings.ToLower(value.text) {
			return "quoted-id:" + strconv.Quote(value.text)
		}
		return "id:" + value.text
	case webhookCheckString:
		return "str:" + strconv.Quote(value.text)
	case webhookCheckInteger:
		return "int:" + value.text
	default:
		return "invalid"
	}
}

func matchingSQLParenthesis(value string, open int) (int, bool) {
	if open < 0 || open >= len(value) || value[open] != '(' {
		return 0, false
	}
	depth := 0
	for index := open; index < len(value); index++ {
		if value[index] == '-' &&
			index+1 < len(value) &&
			value[index+1] == '-' {
			index += 2
			for index < len(value) && value[index] != '\n' {
				index++
			}
			continue
		}
		if value[index] == '/' &&
			index+1 < len(value) &&
			value[index+1] == '*' {
			close := strings.Index(value[index+2:], "*/")
			if close < 0 {
				return 0, false
			}
			index += close + 3
			continue
		}
		switch value[index] {
		case '\'':
			next, ok := skipBalancedSQLQuote(value, index, '\'')
			if !ok {
				return 0, false
			}
			index = next
		case '"':
			next, ok := skipBalancedSQLQuote(value, index, '"')
			if !ok {
				return 0, false
			}
			index = next
		case '`':
			next, ok := skipBalancedSQLQuote(value, index, '`')
			if !ok {
				return 0, false
			}
			index = next
		case '[':
			next := strings.IndexByte(value[index+1:], ']')
			if next < 0 {
				return 0, false
			}
			index += next + 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

func skipBalancedSQLQuote(
	value string,
	start int,
	quote byte,
) (int, bool) {
	for index := start + 1; index < len(value); index++ {
		if value[index] != quote {
			continue
		}
		if index+1 < len(value) && value[index+1] == quote {
			index++
			continue
		}
		return index, true
	}
	return 0, false
}

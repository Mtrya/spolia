package model

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

func ParseDecimal(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty decimal")
	}

	sign := 1
	if value[0] == '+' || value[0] == '-' {
		if value[0] == '-' {
			sign = -1
		}
		value = value[1:]
	}
	if value == "" {
		return nil, fmt.Errorf("invalid decimal")
	}

	mantissa, exponentText := value, ""
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		mantissa, exponentText = value[:index], value[index+1:]
		if strings.IndexAny(exponentText, "eE") >= 0 || exponentText == "" {
			return nil, fmt.Errorf("invalid decimal %q", value)
		}
	}

	whole, fraction := mantissa, ""
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		whole, fraction = mantissa[:index], mantissa[index+1:]
		if strings.IndexByte(fraction, '.') >= 0 {
			return nil, fmt.Errorf("invalid decimal %q", value)
		}
	}
	if whole == "" && fraction == "" {
		return nil, fmt.Errorf("invalid decimal %q", value)
	}
	if whole == "" {
		whole = "0"
	}
	for _, part := range []string{whole, fraction} {
		for _, character := range part {
			if !unicode.IsDigit(character) {
				return nil, fmt.Errorf("invalid decimal %q", value)
			}
		}
	}

	exponent := int64(0)
	if exponentText != "" {
		parsed, ok := new(big.Int).SetString(exponentText, 10)
		if !ok || !parsed.IsInt64() {
			return nil, fmt.Errorf("invalid decimal exponent %q", exponentText)
		}
		exponent = parsed.Int64()
	}
	if exponent > 10000 || exponent < -10000 {
		return nil, fmt.Errorf("decimal exponent out of range")
	}

	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return new(big.Rat), nil
	}
	numerator, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", value)
	}
	if sign < 0 {
		numerator.Neg(numerator)
	}
	scale := int64(len(fraction)) - exponent
	if scale <= 0 {
		numerator.Mul(numerator, powerOfTen(-scale))
		return new(big.Rat).SetInt(numerator), nil
	}
	return new(big.Rat).SetFrac(numerator, powerOfTen(scale)), nil
}

func powerOfTen(exponent int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
}

package mediaprobe

import "time"

func ticksToDuration(value int64, clockRate int) time.Duration {
	if clockRate <= 0 {
		return 0
	}

	seconds := value / int64(clockRate)
	remainder := value % int64(clockRate)
	const (
		maximumDuration = time.Duration(1<<63 - 1)
		minimumDuration = time.Duration(-1 << 63)
	)
	maximumSeconds := int64(maximumDuration / time.Second)
	minimumSeconds := int64(minimumDuration / time.Second)
	if seconds > maximumSeconds {
		return maximumDuration
	}
	if seconds < minimumSeconds {
		return minimumDuration
	}

	whole := time.Duration(seconds) * time.Second
	fraction := time.Duration(
		float64(remainder) * float64(time.Second) / float64(clockRate),
	)
	if fraction > 0 && whole > maximumDuration-fraction {
		return maximumDuration
	}
	if fraction < 0 && whole < minimumDuration-fraction {
		return minimumDuration
	}

	return whole + fraction
}

func buffersSize(buffers [][]byte) (int, bool) {
	maximumInt := int(^uint(0) >> 1)
	total := 0
	for _, buffer := range buffers {
		if len(buffer) > maximumInt-total {
			return 0, false
		}
		total += len(buffer)
	}

	return total, true
}

func rationalRate(numerator, denominator uint64) Rate {
	if numerator == 0 || denominator == 0 {
		return Rate{}
	}

	divisor := greatestCommonDivisor(numerator, denominator)
	numerator /= divisor
	denominator /= divisor
	if numerator > uint64(^uint32(0)) || denominator > uint64(^uint32(0)) {
		return Rate{}
	}

	return Rate{Numerator: uint32(numerator), Denominator: uint32(denominator)}
}

func greatestCommonDivisor(left, right uint64) uint64 {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func mpeg4AudioChannelCount(configuration uint8) int {
	if configuration == 7 {
		return 8
	}

	return int(configuration)
}

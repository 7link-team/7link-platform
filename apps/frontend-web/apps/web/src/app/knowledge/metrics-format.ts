export function qualityRate(rate: {value?: number; denominator: number}, missing: string) {
 return rate.value === undefined ? missing : `${(rate.value * 100).toFixed(1)}% (${rate.denominator})`
}

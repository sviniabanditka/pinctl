/**
 * Formats a numeric device or node value for display.
 *
 * An analog reading is an integer and should look like one; a value computed
 * by an `expression` node is a float and can carry a dozen meaningless digits
 * — a servo angle of `88.67826086956522` says nothing a reader can use, and
 * its width makes the column jump on every tick. Two decimals is enough to see
 * movement, and trailing zeros are dropped so a whole number stays whole.
 */
export function formatNumber(v: number, maxDecimals = 2): string {
  if (!Number.isFinite(v)) return String(v)
  if (Number.isInteger(v)) return String(v)
  return String(Number(v.toFixed(maxDecimals)))
}

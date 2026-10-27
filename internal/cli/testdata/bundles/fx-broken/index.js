export const name = 'fx-broken'
export function apply() {
  throw new Error('fx-broken: boom on apply')
}

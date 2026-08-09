import { connectivityCategories, median } from '../connectivity'
import type { ConnectivityResult, ConnectivityTarget } from '../types'
import { ConnectivityTargetCard } from './ConnectivityTargetCard'

type ConnectivityCategoryProps = {
  category: keyof typeof connectivityCategories
  /** Every target in the category, used for the header's totals. */
  targets: ConnectivityTarget[]
  /** The subset actually listed here; problem targets are lifted to the
      attention section, so this can be shorter than `targets`. */
  visible: ConnectivityTarget[]
  results: Map<string, ConnectivityResult>
  testing: Set<string>
  enforceBaseline: boolean
  onTest: (targetIDs: string[]) => Promise<void>
}

export function ConnectivityCategory({ category, targets, visible, results, testing, enforceBaseline, onTest }: ConnectivityCategoryProps) {
  const meta = connectivityCategories[category]
  const categoryResults = targets.map(target => results.get(target.id)).filter((result): result is ConnectivityResult => Boolean(result))
  const reachable = categoryResults.filter(result => result.status === 'reachable' || result.status === 'degraded').length
  const average = median(categoryResults.map(result => result.median_ms ?? 0).filter(Boolean))
  const busy = targets.some(target => testing.has(target.id))
  const lifted = targets.length - visible.length

  return <section className="connectivity-category">
    <header className="connectivity-category-head">
      <span className="category-mark" aria-hidden="true">{meta.flag}</span>
      <div><h2>{meta.title}</h2><p>{meta.description}</p></div>
      <div className="category-summary">
        <span>{categoryResults.length ? `可达 ${reachable}/${targets.length}${average ? ` · 中位 ${average} ms` : ''}` : '准备检测'}</span>
        <button type="button" disabled={busy} onClick={() => void onTest(targets.map(target => target.id))}>{busy ? '检测中…' : '刷新'}</button>
      </div>
    </header>
    {visible.length
      ? <div className="connectivity-list">{visible.map(target => <ConnectivityTargetCard key={target.id} target={target} result={results.get(target.id)} testing={testing.has(target.id)} enforceBaseline={enforceBaseline} />)}</div>
      : <p className="category-lifted">这一类的 {lifted} 项都需要关注，已列在上方。</p>}
    {visible.length > 0 && lifted > 0 && <p className="category-lifted">另有 {lifted} 项需要关注，已列在上方。</p>}
  </section>
}

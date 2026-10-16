import React, {useEffect, useState} from 'react';

/** @typedef {{today:string,windowStart:string,windowEndExclusive:string,currencies:{currency:string,upcoming:string,average:string}[]}} Summary */

/** @param {{version:number,onExpired:()=>void}} props */
export function ExpenseForecast({version, onExpired}) {
  const [summary, setSummary] = useState(/** @type {Summary|null} */ (null));
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    async function load() {
      try {
        const response = await fetch('/api/summary', {signal:controller.signal});
        if (response.status === 401) { onExpired(); return; }
        if (!response.ok) throw new Error();
        const result = await response.json();
        if (!controller.signal.aborted) setSummary(result);
      } catch {
        if (!controller.signal.aborted) setError('Не удалось загрузить итоги. Проверьте соединение и повторите попытку.');
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void load();
    return () => controller.abort();
  }, [version, retry]);

  return <section className="expense-forecast" aria-label="Прогноз расходов" aria-busy={loading}>
    {loading ? <p role="status">Загружаем итоги…</p> : error ? <div className="error" role="alert"><p>{error}</p><button className="quiet" onClick={() => setRetry(v => v + 1)}>Повторить загрузку итогов</button></div> : summary && <>
      <div className="forecast-cards">
        <section className="panel forecast-card" aria-labelledby="upcoming-title">
          <h2 id="upcoming-title">Платежи в ближайшие 30 дней</h2>
          <p>С <time dateTime={summary.windowStart}>{displayDate(summary.windowStart)}</time> до <time dateTime={summary.windowEndExclusive}>{displayDate(summary.windowEndExclusive)}</time>, не включая последний день.</p>
          {summary.currencies.length ? <dl>{summary.currencies.map(total => <div key={total.currency}><dt className="sr-only">{total.currency}</dt><dd>{total.upcoming} <span>{total.currency}</span></dd></div>)}</dl> : <p className="forecast-empty">Нет активных подписок</p>}
        </section>
        <section className="panel forecast-card" aria-labelledby="average-title">
          <h2 id="average-title">В среднем за 30 дней</h2>
          <p>Средняя нагрузка по всем активным подпискам с учётом их периодичности.</p>
          {summary.currencies.length ? <dl>{summary.currencies.map(total => <div key={total.currency}><dt className="sr-only">{total.currency}</dt><dd>{total.average} <span>{total.currency}</span></dd></div>)}</dl> : <p className="forecast-empty">Нет активных подписок</p>}
        </section>
      </div>
      <p className="forecast-note">Итоги на <time dateTime={summary.today}>{displayDate(summary.today)}</time> по Москве · Каждая валюта отдельно, без пересчёта.</p>
    </>}
  </section>;
}

/** @param {string} date */
function displayDate(date) { return date.split('-').reverse().join('.'); }

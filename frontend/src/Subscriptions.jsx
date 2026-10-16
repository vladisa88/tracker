import React, { useEffect, useRef, useState } from 'react';

/** @typedef {{id:string,name:string,amount:string,currency:string,periodDays:number,nextPaymentDate:string,archived:boolean}} Subscription */
/** @typedef {{periodDays:number[],currencies:{code:string,fractionDigits:number}[],timezone:string,today:string}} Metadata */

/** @param {{csrfToken:string,onExpired:()=>void}} props */
export function Subscriptions({csrfToken, onExpired}) {
  const [items, setItems] = useState(/** @type {Subscription[]} */ ([]));
  const [meta, setMeta] = useState(/** @type {Metadata|null} */ (null));
  const [state, setState] = useState('active');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [open, setOpen] = useState(false);
  const [success, setSuccess] = useState('');
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    async function load() {
      try {
        const responses = await Promise.all([
          fetch('/api/meta', {signal:controller.signal}),
          fetch(`/api/subscriptions?state=${state}`, {signal:controller.signal})
        ]);
        if (responses.some(r => r.status === 401)) { onExpired(); return; }
        if (responses.some(r => !r.ok)) throw new Error();
        const [metadata, subscriptions] = await Promise.all(responses.map(r => r.json()));
        if (!controller.signal.aborted) { setMeta(metadata); setItems(subscriptions); }
      } catch { if (!controller.signal.aborted) setError('Не удалось загрузить подписки. Проверьте соединение и повторите попытку.'); }
      finally { if (!controller.signal.aborted) setLoading(false); }
    }
    void load();
    return () => controller.abort();
  }, [state, version]);
  useEffect(() => {
    const refresh = () => { if (document.visibilityState === 'visible') setVersion(v => v + 1); };
    document.addEventListener('visibilitychange', refresh);
    return () => document.removeEventListener('visibilitychange', refresh);
  }, []);
  return <section className="subscriptions">
    <div className="eyebrow">ЛИЧНОЕ ПРОСТРАНСТВО · ВРЕМЯ ПО МОСКВЕ</div>
    <div className="list-heading"><div><h1>Ближайшие платежи</h1><p className="lead">Регулярные расходы в вашем ритме.</p></div>
      <button className="primary" disabled={!meta || open} onClick={() => {setOpen(true); setSuccess('');}}>Добавить подписку <span aria-hidden="true">＋</span></button></div>
    <p className="schedule-note">Плановые даты переходят автоматически. Они не подтверждают фактическое списание.</p>
    {open && meta && <SubscriptionForm meta={meta} csrfToken={csrfToken} onExpired={onExpired} onCancel={() => setOpen(false)} onSaved={() => {setOpen(false); setSuccess('Подписка добавлена'); setState('active'); setVersion(v => v + 1);}}/>}
    {success && <p className="success" role="status">✓ {success}</p>}
    <div className="list-tabs" role="group" aria-label="Список подписок">
      <button className="quiet" aria-pressed={state === 'active'} onClick={() => setState('active')}>Активные</button>
      <button className="quiet" aria-pressed={state === 'archived'} onClick={() => setState('archived')}>Архив</button>
    </div>
    {loading ? <p role="status">Загружаем подписки…</p> : error ? <div className="error" role="alert"><p>{error}</p><button className="quiet" onClick={() => setVersion(v => v + 1)}>Повторить загрузку</button></div> : items.length === 0 ?
      <section className="panel empty"><span className="empty-icon" aria-hidden="true">↗</span><h2>{state === 'active' ? 'Пока нет подписок' : 'Архив пуст'}</h2><p>{state === 'active' ? 'Добавьте первый регулярный расход — сервис, интернет или аренду.' : 'Здесь будут подписки, исключённые из будущих расходов.'}</p></section> :
      <ul className="subscription-list" aria-label={state === 'active' ? 'Активные подписки' : 'Архивные подписки'}>{items.map(item => <li key={item.id}>
        <span className={`subscription-icon tone-${(item.name.codePointAt(0) || 0) % 4}`} aria-hidden="true">{Array.from(item.name)[0].toUpperCase()}</span>
        <div className="subscription-name">{item.name}<span>Каждые {item.periodDays} дней</span></div>
        <strong className="subscription-amount">{item.amount} <span>{item.currency}</span></strong>
        <div className="subscription-date"><span>Следующий платёж</span><time dateTime={item.nextPaymentDate}>{item.nextPaymentDate.split('-').reverse().join('.')}</time></div>
      </li>)}</ul>}
  </section>;
}

/** @param {{meta:Metadata,csrfToken:string,onExpired:()=>void,onCancel:()=>void,onSaved:()=>void}} props */
function SubscriptionForm({meta, csrfToken, onExpired, onCancel, onSaved}) {
  const [values, setValues] = useState({name:'',amount:'',currency:meta.currencies[0].code,periodDays:String(meta.periodDays[0]),nextPaymentDate:meta.today});
  const [fields, setFields] = useState(/** @type {Record<string,string>} */ ({}));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const form = useRef(/** @type {HTMLFormElement|null} */ (null));
  /** @param {React.FormEvent} event */
  async function save(event) {
    event.preventDefault();
    if (submitting.current) return;
    submitting.current = true; setBusy(true); setFields({}); setError('');
    try {
      const response = await fetch('/api/subscriptions', {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrfToken},body:JSON.stringify({...values,periodDays:Number(values.periodDays)})});
      if (response.status === 401) { onExpired(); return; }
      if (response.status === 201) { onSaved(); return; }
      const result = await response.json();
      setFields(result.error?.fields || {});
      setError(result.error?.message || 'Не удалось сохранить подписку. Повторите попытку.');
      const field = Object.keys(result.error?.fields || {})[0];
      if (field) form.current?.querySelector(/** @type {string} */ (`[name="${field}"]`))?.scrollIntoView({block:'nearest'});
    } catch { setError('Ответ сервера не получен. Проверьте список перед повторной отправкой: подписка могла сохраниться.'); }
    finally { submitting.current = false; setBusy(false); }
  }
  /** @param {React.ChangeEvent<HTMLInputElement|HTMLSelectElement>} event */
  function change(event) { setValues(v => ({...v,[event.target.name]:event.target.value})); }
  /** @param {keyof typeof values} name */
  function attributes(name) { return {id:`subscription-${name}`,name,value:values[name],onChange:change,'aria-invalid':Boolean(fields[name]),'aria-describedby':fields[name] ? `error-${name}` : undefined}; }
  /** @param {keyof typeof values} name */
  function fieldError(name) { return fields[name] && <span className="field-error" id={`error-${name}`}>{fields[name]}</span>; }
  return <form className="panel subscription-form" ref={form} onSubmit={save} noValidate aria-labelledby="form-title" aria-busy={busy}>
    <h2 id="form-title">Новая подписка</h2>
    <fieldset disabled={busy}><div className="form-grid">
      <div className="name-field"><label htmlFor="subscription-name">Название</label><input {...attributes('name')} autoFocus autoComplete="off" placeholder="Например, домашний интернет"/>{fieldError('name')}</div>
      <div><label htmlFor="subscription-amount">Сумма</label><input {...attributes('amount')} inputMode="decimal" placeholder="0.00"/>{fieldError('amount')}</div>
      <div><label htmlFor="subscription-currency">Валюта</label><select {...attributes('currency')}>{meta.currencies.map(c => <option key={c.code} value={c.code}>{c.code}</option>)}</select>{fieldError('currency')}</div>
      <div><label htmlFor="subscription-periodDays">Периодичность</label><select {...attributes('periodDays')}>{meta.periodDays.map(p => <option key={p} value={p}>Каждые {p} дней</option>)}</select>{fieldError('periodDays')}</div>
      <div><label htmlFor="subscription-nextPaymentDate">Следующая дата платежа</label><input {...attributes('nextPaymentDate')} type="date" min={meta.today}/>{fieldError('nextPaymentDate')}</div>
    </div><p className="fine">Периоды считаются в днях, даты могут сдвигаться относительно календарного месяца. Дата — по Москве.</p>
    {error && <p className="field-error" role="alert">{error}</p>}
    <div className="form-actions"><button className="primary" type="submit">{busy ? 'Сохраняем…' : 'Сохранить подписку'}</button><button className="quiet" type="button" onClick={onCancel}>Отмена</button></div></fieldset>
  </form>;
}

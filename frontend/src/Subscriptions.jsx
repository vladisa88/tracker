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
  const [selected, setSelected] = useState(/** @type {Subscription|null} */ (null));
  const [mode, setMode] = useState('create');
  const [archiving, setArchiving] = useState('');
  const [actionError, setActionError] = useState('');
  const actionPending = useRef(false);
  const trigger = useRef(/** @type {HTMLButtonElement|null} */ (null));
  const heading = useRef(/** @type {HTMLHeadingElement|null} */ (null));
  /** @param {string} action @param {Subscription|null} item @param {HTMLButtonElement} button */
  function openForm(action, item, button) {
    trigger.current = button; setMode(action); setSelected(item); setOpen(true); setSuccess(''); setActionError('');
  }
  function closeForm() { setOpen(false); requestAnimationFrame(() => trigger.current?.focus()); }
  /** @param {Subscription} item */
  async function archive(item) {
    if (actionPending.current) return;
    actionPending.current = true; setArchiving(item.id); setSuccess(''); setActionError('');
    try {
      const response = await fetch(`/api/subscriptions/${item.id}/archive`, {method:'POST',headers:{'X-CSRF-Token':csrfToken}});
      if (response.status === 401) { onExpired(); return; }
      if (!response.ok) { const result = await response.json(); setActionError(result.error?.message || 'Не удалось архивировать подписку. Повторите попытку.'); return; }
      setSuccess('Подписка в архиве'); setVersion(v => v + 1); heading.current?.focus();
    } catch { setActionError('Ответ сервера не получен. Проверьте список перед повторной попыткой: подписка могла попасть в архив.'); }
    finally { actionPending.current = false; setArchiving(''); }
  }
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
    <div className="list-heading"><div><h1 ref={heading} tabIndex={-1}>Ближайшие платежи</h1><p className="lead">Регулярные расходы в вашем ритме.</p></div>
      <button className="primary" disabled={!meta || open || Boolean(archiving)} onClick={event => openForm('create', null, event.currentTarget)}>Добавить подписку <span aria-hidden="true">＋</span></button></div>
    <p className="schedule-note">Плановые даты переходят автоматически. Они не подтверждают фактическое списание.</p>
    {open && meta && <SubscriptionForm key={`${mode}-${selected?.id || 'new'}`} meta={meta} item={selected} mode={mode} csrfToken={csrfToken} onExpired={onExpired} onCancel={closeForm} onSaved={() => {setOpen(false); setSuccess(mode === 'create' ? 'Подписка добавлена' : mode === 'restore' ? 'Подписка восстановлена' : 'Изменения сохранены'); if (mode !== 'edit') setState('active'); setVersion(v => v + 1); heading.current?.focus();}}/>}
    {actionError && <p className="error" role="alert">{actionError}</p>}
    {success && <p className="success" role="status">✓ {success}</p>}
    <div className="list-tabs" role="group" aria-label="Список подписок">
      <button className="quiet" aria-pressed={state === 'active'} disabled={open || Boolean(archiving)} onClick={() => {setState('active'); setSuccess(''); setActionError('');}}>Активные</button>
      <button className="quiet" aria-pressed={state === 'archived'} disabled={open || Boolean(archiving)} onClick={() => {setState('archived'); setSuccess(''); setActionError('');}}>Архив</button>
    </div>
    {loading ? <p role="status">Загружаем подписки…</p> : error ? <div className="error" role="alert"><p>{error}</p><button className="quiet" onClick={() => setVersion(v => v + 1)}>Повторить загрузку</button></div> : items.length === 0 ?
      <section className="panel empty"><span className="empty-icon" aria-hidden="true">↗</span><h2>{state === 'active' ? 'Пока нет подписок' : 'Архив пуст'}</h2><p>{state === 'active' ? 'Добавьте первый регулярный расход — сервис, интернет или аренду.' : 'Здесь будут подписки, исключённые из будущих расходов.'}</p></section> :
      <ul className="subscription-list" aria-label={state === 'active' ? 'Активные подписки' : 'Архивные подписки'}>{items.map(item => <li key={item.id}>
        <span className={`subscription-icon tone-${(item.name.codePointAt(0) || 0) % 4}`} aria-hidden="true">{Array.from(item.name)[0].toUpperCase()}</span>
        <div className="subscription-name">{item.name}<span>Каждые {item.periodDays} дней</span></div>
        <strong className="subscription-amount">{item.amount} <span>{item.currency}</span></strong>
        <div className="subscription-date"><span>Следующий платёж</span><time dateTime={item.nextPaymentDate}>{item.nextPaymentDate.split('-').reverse().join('.')}</time></div>
        <div className="subscription-actions" role="group" aria-label={`Действия: ${item.name}`}>
          <button className="quiet" disabled={open || Boolean(archiving) || !meta} onClick={event => openForm('edit', item, event.currentTarget)}>Редактировать</button>
          {item.archived ? <button className="quiet" disabled={open || Boolean(archiving) || !meta} onClick={event => openForm('restore', item, event.currentTarget)}>Восстановить</button> :
            <button className="quiet" disabled={open || Boolean(archiving)} onClick={() => void archive(item)}>{archiving === item.id ? 'Архивируем…' : 'В архив'}</button>}
        </div>
      </li>)}</ul>}
  </section>;
}

/** @param {{meta:Metadata,item:Subscription|null,mode:string,csrfToken:string,onExpired:()=>void,onCancel:()=>void,onSaved:()=>void}} props */
function SubscriptionForm({meta, item, mode, csrfToken, onExpired, onCancel, onSaved}) {
  const [values, setValues] = useState({name:item?.name || '',amount:item?.amount || '',currency:item?.currency || meta.currencies[0].code,periodDays:String(item?.periodDays || meta.periodDays[0]),nextPaymentDate:mode === 'restore' ? '' : item?.nextPaymentDate || meta.today});
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
      // Omit an unchanged date so the server retains the current schedule even across midnight.
      const {nextPaymentDate, ...details} = values;
      const date = mode === 'edit' && nextPaymentDate === item?.nextPaymentDate ? {} : {nextPaymentDate};
      const payload = mode === 'restore' ? {nextPaymentDate} : {...details,periodDays:Number(values.periodDays),...date};
      const path = mode === 'create' ? '/api/subscriptions' : `/api/subscriptions/${item?.id}${mode === 'restore' ? '/restore' : ''}`;
      const response = await fetch(path, {method:mode === 'edit' ? 'PATCH' : 'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrfToken},body:JSON.stringify(payload)});
      if (response.status === 401) { onExpired(); return; }
      if (response.status === (mode === 'create' ? 201 : 200)) { onSaved(); return; }
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
    <h2 id="form-title">{mode === 'create' ? 'Новая подписка' : mode === 'edit' ? 'Редактирование подписки' : `Восстановление: ${item?.name}`}</h2>
    <fieldset disabled={busy}><div className="form-grid">
      {mode !== 'restore' && <><div className="name-field"><label htmlFor="subscription-name">Название</label><input {...attributes('name')} autoFocus autoComplete="off" placeholder="Например, домашний интернет"/>{fieldError('name')}</div>
      <div><label htmlFor="subscription-amount">Сумма</label><input {...attributes('amount')} inputMode="decimal" placeholder="0.00"/>{fieldError('amount')}</div>
      <div><label htmlFor="subscription-currency">Валюта</label><select {...attributes('currency')}>{meta.currencies.map(c => <option key={c.code} value={c.code}>{c.code}</option>)}</select>{fieldError('currency')}</div>
      <div><label htmlFor="subscription-periodDays">Периодичность</label><select {...attributes('periodDays')}>{meta.periodDays.map(p => <option key={p} value={p}>Каждые {p} дней</option>)}</select>{fieldError('periodDays')}</div></>}
      <div><label htmlFor="subscription-nextPaymentDate">Следующая дата платежа</label><input {...attributes('nextPaymentDate')} type="date" min={meta.today} autoFocus={mode === 'restore'}/>{fieldError('nextPaymentDate')}</div>
    </div><p className="fine">Периоды считаются в днях, даты могут сдвигаться относительно календарного месяца. Дата — по Москве.</p>
    {error && <p className="field-error" role="alert">{error}</p>}
    <div className="form-actions"><button className="primary" type="submit">{busy ? 'Сохраняем…' : mode === 'create' ? 'Сохранить подписку' : mode === 'edit' ? 'Сохранить изменения' : 'Восстановить подписку'}</button><button className="quiet" type="button" onClick={onCancel}>Отмена</button></div></fieldset>
  </form>;
}

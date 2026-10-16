import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import { Subscriptions } from './Subscriptions';

/** @typedef {{owner: {telegramId: string}, csrfToken: string}} Session */
function App() {
  const [session, setSession] = useState(/** @type {Session | null} */ (null));
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(new URLSearchParams(location.search).has('login') ? 'Вход не выполнен. Доступ разрешён только владельцу. Попробуйте снова.' : '');
  async function refresh() {
    try {
      const response = await fetch('/api/session');
      if (response.status === 401) setSession(null);
      else if (response.ok) setSession(await response.json());
      else throw new Error();
    } catch { setError('Не удалось связаться с приложением. Проверьте соединение и повторите попытку.'); }
    finally { setLoading(false); }
  }
  useEffect(() => {
    history.replaceState(null, '', '/');
    void refresh();
    const check = () => { if (document.visibilityState === 'visible') void refresh(); };
    document.addEventListener('visibilitychange', check);
    return () => document.removeEventListener('visibilitychange', check);
  }, []);
  async function logout() {
    if (!session) return;
    setBusy(true); setError('');
    try {
      const response = await fetch('/api/logout', {method: 'POST', headers: {'X-CSRF-Token': session.csrfToken}});
      if (response.ok || response.status === 401) setSession(null);
      else throw new Error();
    } catch { setError('Не удалось выйти. Повторите попытку.'); }
    finally { setBusy(false); }
  }
  return <div className="page">
    <header><a className="brand" href="/" aria-label="Рядом — на главную"><span className="mark" aria-hidden="true">р.</span>рядом<span className="brand-caption">регулярные расходы</span></a>
      {session && <button className="quiet" onClick={logout} disabled={busy}>{busy ? 'Выходим…' : 'Выйти'}</button>}
    </header>
    <main>
      {loading ? <section className="panel" role="status"><p>Загружаем приложение…</p></section> : session ? <>
        <Subscriptions csrfToken={session.csrfToken} onExpired={() => setSession(null)}/>
      </> : <div className="login-layout"><section><div className="eyebrow">ЧУТЬ МЕНЬШЕ ДЕРЖАТЬ В ГОЛОВЕ</div><h1>Регулярные расходы.<br/><span>Спокойные планы.</span></h1><p className="lead">Личное место для подписок и ожидаемых расходов.</p><div className="note"><span aria-hidden="true">↗</span><p>Планируйте заранее.<br/><span className="muted">Без учёта фактических списаний.</span></p></div></section>
        <section className="panel login"><div className="tag">ТОЛЬКО ДЛЯ ВЛАДЕЛЬЦА</div><h2>С возвращением</h2><p>Войдите через свой аккаунт Telegram, чтобы открыть приложение.</p><a className="primary" href="/api/auth/telegram/start">Войти через Telegram <span aria-hidden="true">↗</span></a><p className="fine">Доступ разрешён одному заранее указанному аккаунту.</p></section></div>}
      {error && <div className="error" role="alert"><p>{error}</p><button className="quiet" onClick={() => {setError(''); void refresh();}}>Повторить</button></div>}
    </main><footer>Рядом <span>Меньше забот о регулярном.</span></footer>
  </div>;
}
const root = document.getElementById('root');
if (root) createRoot(root).render(<App/>);

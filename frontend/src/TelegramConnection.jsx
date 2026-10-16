import React, { useEffect, useState } from 'react';
import './telegram.css';

/** @typedef {{status: 'disconnected'|'checking'|'connected'|'error', botUrl: string}} Connection */
export function TelegramConnection() {
  const [connection, setConnection] = useState(/** @type {Connection|null} */ (null));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const response = await fetch('/api/telegram/connection');
        if (!response.ok) throw new Error();
        const result = await response.json();
        if (active) { setConnection(result); setError(''); }
      } catch { if (active) setError('Не удалось проверить подключение. Попробуйте ещё раз.'); }
    }
    void load();
    const timer = window.setInterval(() => { if (document.visibilityState === 'visible') void load(); }, 10000);
    const visible = () => { if (document.visibilityState === 'visible') void load(); };
    document.addEventListener('visibilitychange', visible);
    return () => { active = false; window.clearInterval(timer); document.removeEventListener('visibilitychange', visible); };
  }, []);
  async function refresh() {
    setBusy(true);
    try {
      const response = await fetch('/api/telegram/connection');
      if (!response.ok) throw new Error();
      setConnection(await response.json()); setError('');
    } catch { setError('Не удалось проверить подключение. Попробуйте ещё раз.'); }
    finally { setBusy(false); }
  }
  const status = connection?.status;
  return <section className="panel telegram-connection" aria-labelledby="telegram-title">
    <div className="tag">НАПОМИНАНИЯ В TELEGRAM</div>
    <h2 id="telegram-title">{status === 'connected' ? 'Бот подключён' : status === 'error' ? 'Бот недоступен' : status === 'checking' ? 'Проверяем связь с ботом' : 'Подключите бота'}</h2>
    <p role="status">{error ? 'Состояние подключения пока неизвестно.' : !connection ? 'Проверяем подключение…' : status === 'connected' ? 'Проверочное сообщение отправлено в ваш личный чат.' : status === 'error' ? 'Не удалось доставить проверочное сообщение. Разблокируйте бота и отправьте /start ещё раз.' : status === 'checking' ? 'Telegram временно недоступен. Повторим проверочную отправку автоматически.' : 'Вход через Telegram не подключает напоминания. Это отдельный шаг.'}</p>
    {connection && <>
      {status !== 'connected' && <ol><li>Откройте бота в своём аккаунте владельца.</li><li>Нажмите «Начать» или отправьте <code>/start</code> в личном чате.</li><li>Дождитесь проверочного сообщения и проверьте подключение здесь.</li></ol>}
      <a className="primary" href={connection.botUrl} target="_blank" rel="noopener noreferrer">Открыть бота <span aria-hidden="true">↗</span></a>
    </>}
    {error && <p className="telegram-error" role="alert">{error}</p>}
    <button className="quiet" onClick={refresh} disabled={busy}>{busy ? 'Проверяем…' : 'Проверить подключение'}</button>
  </section>;
}

import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";

const api = {
  async listLocks() {
    const response = await fetch("/api/locks");
    return parseResponse(response);
  },
  async createLock(payload) {
    const response = await fetch("/api/locks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    return parseResponse(response);
  },
  async updateUnlockTime(id, payload) {
    const response = await fetch(`/api/locks/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    return parseResponse(response);
  },
  async remove(id) {
    const response = await fetch(`/api/locks/${id}`, {
      method: "DELETE",
      headers: {
        "Content-Type": "application/json",
        "X-Delete-Confirmation": "delete"
      },
      body: JSON.stringify({ confirmation: "delete" })
    });
    return parseResponse(response);
  }
};

async function parseResponse(response) {
  if (response.status === 204) return null;
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || "リクエストに失敗しました");
  return data;
}

function defaultUnlockTime() {
  const date = new Date();
  date.setSeconds(0, 0);
  date.setMinutes(date.getMinutes() - date.getTimezoneOffset());
  return date.toISOString().slice(0, 16);
}

function timezoneName() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "Local";
}

function timezoneOffsetMinutes(localValue) {
  return new Date(localValue).getTimezoneOffset();
}

function localToRFC3339(localValue) {
  return new Date(localValue).toISOString();
}

function formatDate(value) {
  return new Intl.DateTimeFormat("ja-JP", {
    dateStyle: "medium",
    timeStyle: "short"
  }).format(new Date(value));
}

function formatUnlockTime(lock) {
  if (lock.unlockLocal) {
    return `${lock.unlockLocal.replace("T", " ")} (${lock.timezoneName})`;
  }
  return formatDate(lock.unlockAt);
}

function getRemaining(unlockAt) {
  return new Date(unlockAt).getTime() - Date.now();
}

function formatRemaining(ms) {
  if (ms <= 0) return "開封できます";
  const totalSeconds = Math.ceil(ms / 1000);
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (days > 0) return `${days}日 ${hours}時間 ${minutes}分`;
  return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function statusFor(lock) {
  if (lock.unlocked) return "開封済み";
  if (lock.isOpen || getRemaining(lock.unlockAt) <= 0) return "時間で開封可能";
  return "ロック中";
}

function App() {
  const [locks, setLocks] = useState([]);
  const [lockName, setLockName] = useState("");
  const [secretText, setSecretText] = useState("");
  const [unlockAt, setUnlockAt] = useState(defaultUnlockTime());
  const [unlockAtTouched, setUnlockAtTouched] = useState(false);
  const [message, setMessage] = useState("");
  const [deleteDialog, setDeleteDialog] = useState(null);
  const [deleteText, setDeleteText] = useState("");
  const [copiedLockId, setCopiedLockId] = useState(null);
  const [unlockEdits, setUnlockEdits] = useState({});
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    bootstrap().finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    const id = setInterval(() => setTick((value) => value + 1), 1000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    if (!unlockAtTouched && !secretText) {
      setUnlockAt(defaultUnlockTime());
    }
  }, [tick, unlockAtTouched, secretText]);

  useEffect(() => {
    if (locks.some((lock) => !lock.unlocked && !lock.isOpen && getRemaining(lock.unlockAt) <= 0)) {
      refreshAll();
    }
  }, [tick, locks]);

  async function bootstrap() {
    const params = new URLSearchParams(window.location.search);
    if (params.get("checkout_session_id") || params.get("checkout_cancelled")) {
      window.history.replaceState({}, "", window.location.pathname);
    }

    await refreshAll();
  }

  async function refreshAll() {
    const lockData = await api.listLocks();
    setLocks(lockData.locks);
  }

  async function handleCreate(event) {
    event.preventDefault();
    setMessage("");

    const trimmed = secretText.trim();
    if (!trimmed) {
      setMessage("中身を入力してください。");
      return;
    }
    if (!unlockAt) {
      setMessage("開封日時を入力してください。");
      return;
    }

    try {
      await api.createLock({
        name: lockName.trim(),
        secretText: trimmed,
        unlockAt: localToRFC3339(unlockAt),
        unlockLocal: unlockAt,
        timezoneName: timezoneName(),
        timezoneOffsetMinutes: timezoneOffsetMinutes(unlockAt)
      });
      setLockName("");
      setSecretText("");
      setUnlockAt(defaultUnlockTime());
      setUnlockAtTouched(false);
      setMessage("ロックを作成しました。");
      await refreshAll();
    } catch (error) {
      setMessage(error.message);
    }
  }

  function requestDelete(item) {
    setDeleteText("");
    setDeleteDialog(item);
  }

  async function confirmDelete() {
    if (deleteText !== "delete" || !deleteDialog) return;
    setMessage("");

    try {
      await api.remove(deleteDialog.id);
      setMessage("ロックを削除しました。");
      setDeleteDialog(null);
      setDeleteText("");
      await refreshAll();
    } catch (error) {
      setMessage(error.message);
    }
  }

  async function copySecretText(lock) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(lock.secretText);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = lock.secretText;
        textarea.setAttribute("readonly", "");
        textarea.style.position = "fixed";
        textarea.style.top = "-9999px";
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand("copy");
        document.body.removeChild(textarea);
      }

      setCopiedLockId(lock.id);
      window.setTimeout(() => {
        setCopiedLockId((current) => (current === lock.id ? null : current));
      }, 1600);
    } catch {
      setMessage("コピーできませんでした。");
    }
  }

  function editableUnlockValue(lock) {
    return unlockEdits[lock.id] ?? lock.unlockLocal ?? defaultUnlockTime();
  }

  function setEditableUnlockValue(lock, value) {
    setUnlockEdits((current) => ({ ...current, [lock.id]: value }));
  }

  async function handleUpdateUnlockTime(lock) {
    const nextUnlockAt = editableUnlockValue(lock);
    if (!nextUnlockAt) {
      setMessage("開封日時を入力してください。");
      return;
    }

    setMessage("");
    try {
      await api.updateUnlockTime(lock.id, {
        unlockAt: localToRFC3339(nextUnlockAt),
        unlockLocal: nextUnlockAt,
        timezoneName: timezoneName(),
        timezoneOffsetMinutes: timezoneOffsetMinutes(nextUnlockAt)
      });
      setUnlockEdits((current) => {
        const next = { ...current };
        delete next[lock.id];
        return next;
      });
      setMessage("開封日時を更新しました。");
      await refreshAll();
    } catch (error) {
      setMessage(error.message);
    }
  }

  return (
    <main className="app-shell">
      <section className="layout">
        <form className="create-panel" onSubmit={handleCreate}>
          <div>
            <p className="eyebrow">Create</p>
            <h2>新しいロック</h2>
          </div>

          <label className="field">
            <span>名前</span>
            <input
              type="text"
              maxLength="100"
              value={lockName}
              onChange={(event) => setLockName(event.target.value)}
              placeholder="Lock #"
            />
          </label>

          <label className="field">
            <span>中身</span>
            <textarea
              value={secretText}
              onChange={(event) => setSecretText(event.target.value)}
              rows="8"
              placeholder="未来の自分だけが読めるテキスト"
            />
          </label>

          <label className="field">
            <span>開封日時</span>
            <input
              type="datetime-local"
              value={unlockAt}
              onChange={(event) => {
                setUnlockAtTouched(true);
                setUnlockAt(event.target.value);
              }}
            />
          </label>

          <button className="primary-button" type="submit">ロックを作る</button>
          {message && <p className="message">{message}</p>}
        </form>

        <section className="list-panel">
          <div className="section-heading">
            <div>
              <p className="eyebrow">Locks</p>
              <h2>ロック一覧</h2>
            </div>
            <button className="ghost-button" type="button" onClick={refreshAll}>更新</button>
          </div>

          {loading ? (
            <p className="empty">読み込み中...</p>
          ) : locks.length === 0 ? (
            <p className="empty">まだロックはありません。</p>
          ) : (
            <div className="lock-list">
              {locks.map((lock) => {
                const canOpenByTime = lock.isOpen || getRemaining(lock.unlockAt) <= 0;
                const visible = lock.unlocked || canOpenByTime;
                const canChangeUnlockTime = !lock.unlocked && canOpenByTime;

                return (
                  <article className="lock-card" key={lock.id}>
                    <div className="lock-card-header">
                      <div>
                        <span className={`status ${visible ? "open" : "locked"}`}>{statusFor(lock)}</span>
                        <h3>{lock.name || `Lock #${lock.id}`}</h3>
                      </div>
                    </div>

                    <dl className="meta-grid">
                      <div>
                        <dt>開封日時</dt>
                        <dd>{formatUnlockTime(lock)}</dd>
                      </div>
                      <div>
                        <dt>残り</dt>
                        <dd>{formatRemaining(getRemaining(lock.unlockAt))}</dd>
                      </div>
                    </dl>

                    {visible && (
                      <div className="secret-block">
                        <pre className="secret-text">{lock.secretText}</pre>
                        <button className="copy-button" type="button" onClick={() => copySecretText(lock)}>
                          {copiedLockId === lock.id ? "コピー済み" : "コピー"}
                        </button>
                      </div>
                    )}

                    {canChangeUnlockTime && (
                      <div className="relock-panel">
                        <label className="field">
                          <span>開封日時を変更</span>
                          <input
                            type="datetime-local"
                            value={editableUnlockValue(lock)}
                            onChange={(event) => setEditableUnlockValue(lock, event.target.value)}
                          />
                        </label>
                        <button className="ghost-button" type="button" onClick={() => handleUpdateUnlockTime(lock)}>
                          更新
                        </button>
                      </div>
                    )}

                    <div className="card-actions">
                      <button className="danger-button" type="button" onClick={() => requestDelete(lock)}>削除</button>
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </section>
      </section>

      {deleteDialog && (
        <div className="modal-backdrop" role="presentation">
          <div className="confirm-modal" role="dialog" aria-modal="true" aria-labelledby="delete-title">
            <p className="eyebrow">Delete</p>
            <h2 id="delete-title">本当に削除しますか</h2>
            <p className="locked-copy">
              {deleteDialog.name || `Lock #${deleteDialog.id}`} を削除します。
              続けるには「delete」と入力してください。
            </p>
            <input value={deleteText} onChange={(event) => setDeleteText(event.target.value)} placeholder="delete" autoFocus />
            <div className="card-actions">
              <button className="danger-button" type="button" disabled={deleteText !== "delete"} onClick={confirmDelete}>削除を確定</button>
              <button className="ghost-button" type="button" onClick={() => setDeleteDialog(null)}>キャンセル</button>
            </div>
          </div>
        </div>
      )}
    </main>
  );
}

createRoot(document.getElementById("root")).render(<App />);

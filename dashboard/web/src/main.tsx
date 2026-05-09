import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  Eye,
  EyeOff,
  Play,
  Plus,
  RefreshCcw,
  Save,
  Search,
  ShieldCheck,
  Trash2,
  X,
  Zap
} from 'lucide-react';
import './styles.css';

type Account = {
  account_id: string;
  email: string;
  password: string;
  status: string;
  error_message: string;
  session_token: string;
  access_token: string;
  created_at: number;
  updated_at: number;
};

type Job = {
  job_id: string;
  account_id: string;
  action: string;
  status: string;
  recoverable: boolean;
  retryable: boolean;
  last_step: string;
  error_message: string;
  result_json: string;
  created_at: string;
  updated_at: string;
  steps?: Step[];
};

type Step = {
  step_name: string;
  status: string;
  recoverable: boolean;
  retryable: boolean;
  error_message: string;
  result_json: string;
  started_at: number;
  completed_at: number;
};

type Toast = { kind: 'ok' | 'error'; text: string } | null;

const statusOptions = ['', 'CREATED', 'REGISTERED', 'ACTIVATED', 'REGISTER_FAILED', 'PAYMENT_FAILED'];
const jobStatusOptions = ['', 'RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_RECOVERABLE', 'FAILED_FINAL'];

function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selectedAccount, setSelectedAccount] = useState<Account | null>(null);
  const [selectedJob, setSelectedJob] = useState<Job | null>(null);
  const [accountStatus, setAccountStatus] = useState('');
  const [jobStatus, setJobStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<Toast>(null);
  const [showSecrets, setShowSecrets] = useState(false);
  const [runningAccountIds, setRunningAccountIds] = useState<Set<string>>(new Set());
  const [runningJobCount, setRunningJobCount] = useState(0);

  async function refresh() {
    setBusy(true);
    try {
      const [accountsData, jobsData, runningJobsData] = await Promise.all([
        api<Account[]>(`/api/accounts?limit=200${accountStatus ? `&status=${accountStatus}` : ''}`),
        api<Job[]>(`/api/jobs?limit=200${jobStatus ? `&status=${jobStatus}` : ''}`),
        api<Job[]>('/api/jobs?limit=200&status=RUNNING')
      ]);
      setAccounts(Array.isArray(accountsData) ? accountsData : []);
      setJobs(Array.isArray(jobsData) ? jobsData : []);
      const runningJobs = Array.isArray(runningJobsData) ? runningJobsData : [];
      setRunningJobCount(runningJobs.length);
      setRunningAccountIds(new Set(runningJobs.filter((job) => job.account_id).map((job) => job.account_id)));
      if (selectedJob) {
        setSelectedJob(await api<Job>(`/api/jobs/${selectedJob.job_id}`));
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function runAccountWorkflow(label: string, path: string, account: Account) {
    setBusy(true);
    try {
      const resp = await api<any>(path, { method: 'POST', body: JSON.stringify({ account_id: account.account_id }) });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
      } else {
        setToast({ kind: 'ok', text: `${label} 已提交: ${resp.job_id || 'ok'}` });
        await refresh();
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function deleteAccount(account: Account) {
    if (!window.confirm(`删除账号 ${account.email || account.account_id}？`)) return;
    setBusy(true);
    try {
      await api<any>(`/api/accounts/${account.account_id}`, { method: 'DELETE' });
      if (selectedAccount?.account_id === account.account_id) setSelectedAccount(null);
      setToast({ kind: 'ok', text: '账号已删除' });
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function retryJob(job: Job) {
    setBusy(true);
    try {
      const resp = await api<any>(`/api/jobs/${job.job_id}/retry`, { method: 'POST', body: '{}' });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
      } else {
        setToast({ kind: 'ok', text: `流程已重试: ${resp.job_id || 'ok'}` });
        await refresh();
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function updateAccountAuth(account: Account, payload: { session_token?: string; access_token?: string }) {
    setBusy(true);
    try {
      const updated = await api<Account>(`/api/accounts/${account.account_id}`, {
        method: 'PATCH',
        body: JSON.stringify(payload)
      });
      setAccounts((prev) => prev.map((item) => item.account_id === updated.account_id ? updated : item));
      setSelectedAccount(updated);
      setToast({ kind: 'ok', text: '认证信息已更新' });
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    refresh();
    const id = window.setInterval(refresh, 15000);
    return () => window.clearInterval(id);
  }, [accountStatus, jobStatus]);

  useEffect(() => {
    if (!toast) return;
    const id = window.setTimeout(() => setToast(null), toast.kind === 'error' ? 6000 : 3500);
    return () => window.clearTimeout(id);
  }, [toast]);

  function selectAccount(account: Account) {
    setSelectedAccount(account);
    setSelectedJob(null);
  }

  async function selectJob(job: Job) {
    try {
      setSelectedAccount(null);
      setSelectedJob(await api<Job>(`/api/jobs/${job.job_id}`));
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    }
  }

  function closeDetails() {
    setSelectedAccount(null);
    setSelectedJob(null);
  }

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <h1>NB Register</h1>
          <p>账号、注册、激活和 GoPay 工作流控制台</p>
        </div>
        <div className="topbarActions">
          <button className="primaryButton" onClick={refresh} disabled={busy}>
            <RefreshCcw size={16} /> 刷新
          </button>
        </div>
      </header>

      {toast && <div className={`toast ${toast.kind}`} title={toast.text}>{compactToast(toast.text)}</div>}

      <section className="metrics">
        <Metric label="账号" value={accounts.length} icon={<ShieldCheck />} />
        <Metric label="已激活" value={accounts.filter((a) => a.status === 'ACTIVATED').length} icon={<Zap />} />
        <Metric label="运行中 Job" value={runningJobCount} icon={<Activity />} />
        <Metric label="可重试失败" value={jobs.filter((j) => j.retryable).length} icon={<RefreshCcw />} />
      </section>

      <section className="workspace">
        <div className="panel accountsPanel">
          <PanelHeader title="账号" icon={<Search size={16} />}>
            <div className="headerControls">
              <button className="secondaryButton" onClick={() => setShowSecrets((v) => !v)}>
                {showSecrets ? <EyeOff size={16} /> : <Eye size={16} />}
                {showSecrets ? '隐藏' : '显示'}
              </button>
              <select value={accountStatus} onChange={(e) => setAccountStatus(e.target.value)}>
                {statusOptions.map((s) => <option key={s} value={s}>{s || '全部状态'}</option>)}
              </select>
            </div>
          </PanelHeader>
          <CreateAccountForm
            onDone={async (message) => {
              setToast({ kind: 'ok', text: message });
              await refresh();
            }}
            onError={(message) => setToast({ kind: 'error', text: message })}
          />
          <AccountTable
            accounts={accounts}
            selected={selectedAccount?.account_id}
            showSecrets={showSecrets}
            runningAccountIds={runningAccountIds}
            busy={busy}
            onSelect={selectAccount}
            onRegister={(account) => runAccountWorkflow('注册账号', '/api/workflows/register', account)}
            onActivate={(account) => runAccountWorkflow('激活账号', '/api/workflows/activate', account)}
            onRegisterActivate={(account) => runAccountWorkflow('注册并激活', '/api/workflows/register-and-activate', account)}
            onDelete={deleteAccount}
          />
        </div>

        <div className="panel jobsPanel">
          <PanelHeader title="工作流" icon={<Activity size={16} />}>
            <select value={jobStatus} onChange={(e) => setJobStatus(e.target.value)}>
              {jobStatusOptions.map((s) => <option key={s} value={s}>{s || '全部状态'}</option>)}
            </select>
          </PanelHeader>
          <JobTable jobs={jobs} selected={selectedJob?.job_id} busy={busy} onSelect={selectJob} onRetry={retryJob} />
        </div>
      </section>

      <DetailDrawer open={!!selectedAccount} title="账号详情" onClose={closeDetails}>
        {selectedAccount && (
          <AccountDetails
            account={selectedAccount}
            showSecrets={showSecrets}
            onSessionSave={(account, sessionToken) => updateAccountAuth(account, { session_token: sessionToken })}
            onAccessSave={(account, accessToken) => updateAccountAuth(account, { access_token: accessToken })}
          />
        )}
      </DetailDrawer>

      <DetailDrawer open={!!selectedJob} title="工作流详情" onClose={closeDetails}>
        {selectedJob && (
          <JobDetails
            job={selectedJob}
            busy={busy}
            onJobRetry={retryJob}
          />
        )}
      </DetailDrawer>
    </main>
  );
}

function Metric({ label, value, icon }: { label: string; value: number; icon: React.ReactNode }) {
  return (
    <div className="metric">
      <span>{icon}</span>
      <div>
        <strong>{value}</strong>
        <p>{label}</p>
      </div>
    </div>
  );
}

function PanelHeader({ title, icon, children }: { title: string; icon: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="panelHeader">
      <div><span>{icon}</span>{title}</div>
      {children}
    </div>
  );
}

function DetailDrawer({ open, title, onClose, children }: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="drawerLayer open">
      <button className="drawerBackdrop" onClick={onClose} aria-label="关闭详情" />
      <aside className="detailDrawer" role="dialog" aria-modal="true" aria-label={title}>
        <div className="drawerHeader">
          <div><Activity size={16} />{title}</div>
          <button className="iconButton" title="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        {children}
      </aside>
    </div>
  );
}

function AccountDetails({ account, showSecrets, onSessionSave, onAccessSave }: {
  account: Account;
  showSecrets: boolean;
  onSessionSave: (account: Account, sessionToken: string) => Promise<void>;
  onAccessSave: (account: Account, accessToken: string) => Promise<void>;
}) {
  return (
    <div className="details">
      <section>
        <h3>账号</h3>
        <KV label="ID" value={account.account_id} mono />
        <KV label="Status" value={account.status || '-'} />
        <KV label="Email" value={account.email} />
        <KV label="Password" value={showSecrets ? account.password : mask(account.password)} mono />
        <TokenEditor label="Session" field="session_token" account={account} showSecrets={showSecrets} onSave={onSessionSave} />
        <TokenEditor label="Access" field="access_token" account={account} showSecrets={showSecrets} onSave={onAccessSave} />
        <KV label="Created" value={formatUnix(account.created_at)} />
        <KV label="Updated" value={formatUnix(account.updated_at)} />
        <KV label="Error" value={account.error_message || '-'} />
      </section>
    </div>
  );
}

function JobDetails({ job, busy, onJobRetry }: {
  job: Job;
  busy: boolean;
  onJobRetry: (job: Job) => void;
}) {
  return (
    <div className="details">
      <section>
        <div className="sectionTitle">
          <h3>工作流</h3>
          <button disabled={busy || job.status === 'RUNNING'} onClick={() => onJobRetry(job)}>
            <RefreshCcw size={14} /> 重试
          </button>
        </div>
        <KV label="Job" value={job.job_id} mono />
        <KV label="Action" value={job.action} />
        <KV label="Status" value={job.status} />
        <KV label="Error" value={job.error_message || '-'} />
        <div className="timeline">
          {(job.steps || []).map((step) => (
            <div className="step" key={step.step_name}>
              <div>
                <strong>{step.step_name}</strong>
                <StatusBadge status={step.status} retryable={step.retryable} />
              </div>
              {step.error_message && <p>{step.error_message}</p>}
              {step.result_json && <pre>{formatJSON(step.result_json)}</pre>}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
function AccountTable({ accounts, selected, showSecrets, runningAccountIds, busy, onSelect, onRegister, onActivate, onRegisterActivate, onDelete }: {
  accounts: Account[];
  selected?: string;
  showSecrets: boolean;
  runningAccountIds: Set<string>;
  busy: boolean;
  onSelect: (a: Account) => void;
  onRegister: (a: Account) => void;
  onActivate: (a: Account) => void;
  onRegisterActivate: (a: Account) => void;
  onDelete: (a: Account) => void;
}) {
  return (
    <div className="tableWrap">
      <table>
        <thead>
          <tr>
            <th>账号</th>
            <th>密码</th>
            <th>状态</th>
            <th>Session</th>
            <th>Access</th>
            <th>更新</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {accounts.map((account) => {
            const accountBusy = runningAccountIds.has(account.account_id);
            return (
              <tr key={account.account_id} className={selected === account.account_id ? 'selected' : ''} onClick={() => onSelect(account)}>
                <td>
                  <div className="cellStack">
                    <span>{showSecrets ? account.email : mask(account.email)}</span>
                    <small className="mono">{short(account.account_id)}</small>
                  </div>
                </td>
                <td className="secret">{showSecrets ? account.password : mask(account.password)}</td>
                <td><StatusBadge status={account.status} /></td>
                <td className="mono">{showSecrets ? short(account.session_token, 18) : mask(account.session_token)}</td>
                <td className="mono">{showSecrets ? short(account.access_token, 18) : mask(account.access_token)}</td>
                <td>{formatUnix(account.updated_at)}</td>
                <td>
                  <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                    {accountBusy ? (
                      <span className="busyLabel">进行中</span>
                    ) : (
                      <>
                        {canRegister(account) && <button title="注册" disabled={busy} onClick={() => onRegister(account)}><Play size={14} /></button>}
                        {canActivate(account) && <button title="激活" disabled={busy} onClick={() => onActivate(account)}><Zap size={14} /></button>}
                        {canRegister(account) && <button title="注册并激活" disabled={busy} onClick={() => onRegisterActivate(account)}><ShieldCheck size={14} /></button>}
                        <button className="dangerButton" title="删除账号" disabled={busy} onClick={() => onDelete(account)}><Trash2 size={14} /></button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function JobTable({ jobs, selected, busy, onSelect, onRetry }: {
  jobs: Job[];
  selected?: string;
  busy: boolean;
  onSelect: (j: Job) => void;
  onRetry: (j: Job) => void;
}) {
  return (
    <div className="tableWrap">
      <table>
        <thead>
          <tr><th>Job</th><th>动作</th><th>状态</th><th>步骤</th><th>操作</th></tr>
        </thead>
        <tbody>
          {jobs.map((job) => (
            <tr key={job.job_id} className={selected === job.job_id ? 'selected' : ''} onClick={() => onSelect(job)}>
              <td className="mono">{short(job.job_id)}</td>
              <td>{job.action}</td>
              <td><StatusBadge status={job.status} retryable={job.retryable} /></td>
              <td>{job.last_step || '-'}</td>
              <td>
                <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                  <button title="按同参数重试" disabled={busy || job.status === 'RUNNING'} onClick={() => onRetry(job)}>
                    <RefreshCcw size={14} />
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CreateAccountForm({ onDone, onError }: {
  onDone: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [form, setForm] = useState({
    email: '',
    password: ''
  });
  const [working, setWorking] = useState('');

  function update(key: keyof typeof form, value: string) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function run(label: string, path: string, payload: unknown) {
    setWorking(label);
    try {
      const resp = await api<any>(path, { method: 'POST', body: JSON.stringify(payload) });
      if (resp.error_message) {
        onError(resp.error_message);
      } else {
        onDone(`${label} 已提交: ${resp.job_id || resp.account_id || 'ok'}`);
      }
    } catch (err) {
      onError(errorText(err));
    } finally {
      setWorking('');
    }
  }

  return (
    <div className="createAccount">
      <div className="formGrid">
        <input placeholder="邮箱，可空" value={form.email} onChange={(e) => update('email', e.target.value)} />
        <input placeholder="密码，可空" value={form.password} onChange={(e) => update('password', e.target.value)} />
      </div>
      <div className="buttonRow">
        <button onClick={() => run('创建账号', '/api/accounts', form)} disabled={!!working}><Plus size={15} /> 创建账号</button>
      </div>
      {working && <p className="hint">正在执行：{working}</p>}
    </div>
  );
}

function TokenEditor({ label, field, account, showSecrets, onSave }: {
  label: string;
  field: 'session_token' | 'access_token';
  account: Account;
  showSecrets: boolean;
  onSave: (account: Account, token: string) => Promise<void>;
}) {
  const current = account[field] || '';
  const [value, setValue] = useState(current);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setValue(account[field] || '');
  }, [account.account_id, account.session_token, account.access_token, field]);

  async function save() {
    setSaving(true);
    try {
      await onSave(account, value.trim());
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="editLine">
      <span>{label}</span>
      <input
        className="mono"
        type={showSecrets ? 'text' : 'password'}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder={`${label.toLowerCase()} token`}
      />
      <button onClick={save} disabled={saving || value.trim() === current}>
        <Save size={14} /> 保存
      </button>
    </div>
  );
}

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <div className="kv"><span>{label}</span><button className={mono ? 'mono' : ''} onClick={() => navigator.clipboard?.writeText(value)}>{value || '-'}</button></div>;
}

function StatusBadge({ status, retryable }: { status: string; retryable?: boolean }) {
  const cls = status.includes('FAILED') ? 'bad' : status === 'SUCCEEDED' || status === 'ACTIVATED' || status === 'REGISTERED' ? 'good' : 'mid';
  return <span className={`badge ${cls}`}>{status || '-'}{retryable ? ' / retry' : ''}</span>;
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data as T;
}

function canRegister(account: Account) {
  return !hasRegisteredSession(account);
}

function canActivate(account: Account) {
  return account.status !== 'ACTIVATED' && (!!account.session_token || !!account.access_token);
}

function hasRegisteredSession(account: Account) {
  return account.status === 'REGISTERED' || account.status === 'ACTIVATED' || !!account.session_token || !!account.access_token;
}

function short(value: string, size = 8) {
  return value ? `${value.slice(0, size)}…` : '-';
}

function mask(value: string) {
  return value ? '••••••••' : '-';
}

function formatUnix(value: number) {
  return value ? new Date(value * 1000).toLocaleString() : '-';
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err);
}

function compactToast(value: string) {
  const text = String(value || '');
  return text.length > 150 ? `${text.slice(0, 150)}...` : text;
}

function formatJSON(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

createRoot(document.getElementById('root')!).render(<App />);

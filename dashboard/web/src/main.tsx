import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  Copy,
  Database,
  Eye,
  EyeOff,
  Inbox,
  KeyRound,
  ListChecks,
  Mail,
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
  plus_trial_eligible?: boolean;
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

type Mailbox = {
  email_address: string;
  password: string;
  refresh_token: string;
  access_token: string;
  status: string;
  last_error: string;
  is_primary: boolean;
  primary_email: string;
  created_at: number;
  updated_at: number;
};

type MailboxOAuthResponse = {
  started: boolean;
  job_id: string;
  error_message: string;
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
type ViewKey = 'accounts' | 'mailboxes' | 'mailboxRegistration' | 'jobs';

const statusOptions = ['', 'CREATED', 'REGISTERED', 'ACTIVATED', 'EMAIL_ALREADY_EXISTS', 'REGISTER_FAILED', 'PAYMENT_FAILED'];
const jobStatusOptions = ['', 'RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_RECOVERABLE', 'FAILED_FINAL'];
const mailboxStatusOptions = ['', 'AVAILABLE', 'ASSIGNED', 'REGISTERED', 'OAUTH_PENDING', 'USER_ALREADY_EXISTS', 'REGISTRATION_FAILED', 'AUTH_FAILED', 'BLOCKED'];

function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [activeView, setActiveView] = useState<ViewKey>('accounts');
  const [selectedAccount, setSelectedAccount] = useState<Account | null>(null);
  const [selectedJob, setSelectedJob] = useState<Job | null>(null);
  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);
  const [accountStatus, setAccountStatus] = useState('');
  const [jobStatus, setJobStatus] = useState('');
  const [mailboxStatus, setMailboxStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<Toast>(null);
  const [showSecrets, setShowSecrets] = useState(false);
  const [mailboxRegistering, setMailboxRegistering] = useState(false);
  const [mailboxOAuthing, setMailboxOAuthing] = useState('');
  const [runningAccountIds, setRunningAccountIds] = useState<Set<string>>(new Set());
  const [refreshingAccessTokenIds, setRefreshingAccessTokenIds] = useState<Set<string>>(new Set());
  const [runningJobCount, setRunningJobCount] = useState(0);

  async function refresh() {
    setBusy(true);
    try {
      const [accountsData, jobsData, mailboxesData, runningJobsData] = await Promise.all([
        api<Account[]>(`/api/accounts?limit=200${accountStatus ? `&status=${accountStatus}` : ''}`),
        api<Job[]>(`/api/jobs?limit=200${jobStatus ? `&status=${jobStatus}` : ''}`),
        api<Mailbox[]>(`/api/mailboxes?limit=200${mailboxStatus ? `&status=${mailboxStatus}` : ''}`),
        api<Job[]>('/api/jobs?limit=200&status=RUNNING')
      ]);
      setAccounts(Array.isArray(accountsData) ? accountsData : []);
      setJobs(Array.isArray(jobsData) ? jobsData : []);
      const nextMailboxes = Array.isArray(mailboxesData) ? mailboxesData : [];
      setMailboxes(nextMailboxes);
      const runningJobs = Array.isArray(runningJobsData) ? runningJobsData : [];
      setRunningJobCount(runningJobs.length);
      setRunningAccountIds(new Set(runningJobs.filter((job) => job.account_id).map((job) => job.account_id)));
      if (selectedJob) {
        setSelectedJob(await api<Job>(`/api/jobs/${selectedJob.job_id}`));
      }
      if (selectedMailbox) {
        const freshMailbox = nextMailboxes.find((mailbox) => mailbox.email_address === selectedMailbox.email_address);
        if (freshMailbox) setSelectedMailbox(freshMailbox);
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

  async function submitJobOtp(job: Job, otp: string) {
    try {
      const resp = await api<{ success: boolean; job_id: string; error_message?: string }>(`/api/jobs/${job.job_id}/otp`, {
        method: 'POST',
        body: JSON.stringify({ otp })
      });
      if (resp.error_message || !resp.success) {
        setToast({ kind: 'error', text: resp.error_message || 'OTP 提交失败' });
        return;
      }
      setToast({ kind: 'ok', text: `OTP 已提交: ${short(resp.job_id || job.job_id)}` });
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    }
  }

  async function startMailboxRegistration() {
    setMailboxRegistering(true);
    try {
      const resp = await api<{ started: boolean }>('/api/mailboxes/register', { method: 'POST', body: '{}' });
      setToast({ kind: resp.started ? 'ok' : 'error', text: resp.started ? '手动注册邮箱已启动' : '手动注册邮箱未启动' });
      window.setTimeout(refresh, 3000);
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setMailboxRegistering(false);
    }
  }

  async function runMailboxOAuth(emailAddress = '') {
    setMailboxOAuthing(emailAddress || '*');
    try {
      const resp = await api<MailboxOAuthResponse>('/api/mailboxes/oauth', {
        method: 'POST',
        body: JSON.stringify({
          email_address: emailAddress,
          only_missing: !emailAddress,
          limit: 100
        })
      });
      if (!resp.started || resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message || 'OAuth 流程启动失败' });
      } else {
        setToast({ kind: 'ok', text: `OAuth 流程已提交: ${short(resp.job_id)}` });
      }
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setMailboxOAuthing('');
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

  async function refreshAccountAccessToken(account: Account) {
    setRefreshingAccessTokenIds((prev) => new Set(prev).add(account.account_id));
    try {
      const updated = await api<Account>(`/api/accounts/${account.account_id}/access-token`, {
        method: 'POST',
        body: '{}'
      });
      setAccounts((prev) => prev.map((item) => item.account_id === updated.account_id ? updated : item));
      if (selectedAccount?.account_id === updated.account_id) setSelectedAccount(updated);
      setToast({ kind: 'ok', text: 'Access Token 已自动获取' });
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    } finally {
      setRefreshingAccessTokenIds((prev) => {
        const next = new Set(prev);
        next.delete(account.account_id);
        return next;
      });
    }
  }

  useEffect(() => {
    refresh();
    const id = window.setInterval(refresh, 15000);
    return () => window.clearInterval(id);
  }, [accountStatus, jobStatus, mailboxStatus]);

  useEffect(() => {
    if (!toast) return;
    const id = window.setTimeout(() => setToast(null), toast.kind === 'error' ? 6000 : 3500);
    return () => window.clearTimeout(id);
  }, [toast]);

  function selectAccount(account: Account) {
    setSelectedAccount(account);
    setSelectedJob(null);
    setSelectedMailbox(null);
  }

  async function selectJob(job: Job) {
    try {
      setSelectedAccount(null);
      setSelectedMailbox(null);
      setSelectedJob(await api<Job>(`/api/jobs/${job.job_id}`));
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    }
  }

  function selectMailbox(mailbox: Mailbox) {
    setSelectedAccount(null);
    setSelectedJob(null);
    setSelectedMailbox(mailbox);
  }

  function closeDetails() {
    setSelectedAccount(null);
    setSelectedJob(null);
    setSelectedMailbox(null);
  }

  function openView(view: ViewKey) {
    setActiveView(view);
    closeDetails();
  }

  const missingOAuthCount = mailboxes.filter((mailbox) => mailbox.is_primary && mailbox.password && !mailbox.refresh_token).length;
  const mailboxRegisterJobs = jobs.filter((job) => job.action === 'REGISTER_MAILBOX');
  const mailboxOAuthJobs = jobs.filter((job) => job.action === 'MAILBOX_OAUTH');
  const runningMailboxRegisterCount = mailboxRegisterJobs.filter((job) => job.status === 'RUNNING').length;

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

      <section className="appFrame">
        <nav className="navRail" aria-label="主导航">
          <NavItem active={activeView === 'accounts'} icon={<Database size={17} />} label="账号" count={accounts.length} onClick={() => openView('accounts')} />
          <NavItem active={activeView === 'mailboxes'} icon={<Inbox size={17} />} label="邮箱管理" count={mailboxes.filter((m) => m.status === 'AVAILABLE').length} onClick={() => openView('mailboxes')} />
          <NavItem active={activeView === 'mailboxRegistration'} icon={<Play size={17} />} label="邮箱注册" count={runningMailboxRegisterCount} onClick={() => openView('mailboxRegistration')} />
          <NavItem active={activeView === 'jobs'} icon={<ListChecks size={17} />} label="工作流" count={runningJobCount} onClick={() => openView('jobs')} />
        </nav>

        <div className="contentPane">
          <section className="metrics">
            <Metric label="账号" value={accounts.length} icon={<ShieldCheck />} />
            <Metric label="已激活" value={accounts.filter((a) => a.status === 'ACTIVATED').length} icon={<Zap />} />
            <Metric label="可用邮箱" value={mailboxes.filter((m) => m.status === 'AVAILABLE').length} icon={<Mail />} />
            <Metric label="运行中 Job" value={runningJobCount} icon={<Activity />} />
            <Metric label="可重试失败" value={jobs.filter((j) => j.retryable).length} icon={<RefreshCcw />} />
          </section>

          {activeView === 'accounts' && (
            <section className="workspace accountsWorkspace">
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
                  refreshingAccessTokenIds={refreshingAccessTokenIds}
                  busy={busy}
                  onSelect={selectAccount}
                  onRegister={(account) => runAccountWorkflow('注册账号', '/api/workflows/register', account)}
                  onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
                  onActivate={(account) => runAccountWorkflow('激活账号', '/api/workflows/activate', account)}
                  onProbePlusTrial={(account) => runAccountWorkflow('资格探测', '/api/workflows/probe-plus-trial', account)}
                  onRegisterActivate={(account) => runAccountWorkflow('注册并激活', '/api/workflows/register-and-activate', account)}
                  onRefreshAccessToken={refreshAccountAccessToken}
                  onDelete={deleteAccount}
                />
              </div>

              <div className="panel jobsPanel compactPanel">
                <PanelHeader title="最近工作流" icon={<Activity size={16} />}>
                  <button className="secondaryButton" onClick={() => openView('jobs')}>查看全部</button>
                </PanelHeader>
                <JobTable jobs={jobs.slice(0, 8)} selected={selectedJob?.job_id} busy={busy} onSelect={selectJob} onRetry={retryJob} />
              </div>
            </section>
          )}

          {activeView === 'mailboxes' && (
            <section className="workspace mailboxWorkspace">
              <div className="panel mailboxesPanel">
                <PanelHeader title="邮箱管理" icon={<Mail size={16} />}>
                  <div className="headerControls">
                    <button className="secondaryButton" onClick={() => runMailboxOAuth()} disabled={busy || !!mailboxOAuthing || missingOAuthCount === 0}>
                      <KeyRound size={16} /> 补 OAuth {missingOAuthCount > 0 ? `(${missingOAuthCount})` : ''}
                    </button>
                    <button className="secondaryButton" onClick={() => setShowSecrets((v) => !v)}>
                      {showSecrets ? <EyeOff size={16} /> : <Eye size={16} />}
                      {showSecrets ? '隐藏' : '显示'}
                    </button>
                    <select value={mailboxStatus} onChange={(e) => setMailboxStatus(e.target.value)}>
                      {mailboxStatusOptions.map((s) => <option key={s} value={s}>{s || '全部状态'}</option>)}
                    </select>
                  </div>
                </PanelHeader>
                <MailboxPanel
                  mailboxes={mailboxes}
	                  selected={selectedMailbox?.email_address}
	                  busy={busy}
	                  showSecrets={showSecrets}
                    oauthing={mailboxOAuthing}
	                  onSelect={selectMailbox}
                    onOAuth={runMailboxOAuth}
	                  onDone={async (message) => {
	                    setToast({ kind: 'ok', text: message });
	                    await refresh();
                  }}
                  onError={(message) => setToast({ kind: 'error', text: message })}
                />
                {mailboxOAuthJobs.length > 0 && (
                  <>
                    <div className="sectionTitle">
                      <h3>OAuth Job</h3>
                      <button className="secondaryButton" onClick={() => openView('jobs')}>
                        <ListChecks size={14} /> 全部工作流
                      </button>
                    </div>
                    <JobTable jobs={mailboxOAuthJobs.slice(0, 10)} selected={selectedJob?.job_id} busy={busy} onSelect={selectJob} onRetry={retryJob} />
                  </>
                )}
	              </div>
	            </section>
	          )}

	          {activeView === 'mailboxRegistration' && (
	            <section className="workspace mailboxRegistrationWorkspace">
	              <div className="panel mailboxRegisterPanel">
	                <PanelHeader title="邮箱注册" icon={<Play size={16} />}>
	                  <div className="headerControls">
	                    <button className="primaryButton" onClick={startMailboxRegistration} disabled={busy || mailboxRegistering}>
	                      <Play size={16} /> 启动注册
	                    </button>
	                    <button className="secondaryButton" onClick={() => openView('mailboxes')}>
	                      <Inbox size={16} /> 邮箱管理
	                    </button>
	                  </div>
	                </PanelHeader>
	                <div className="mailboxRegisterBody">
	                  <MailboxStatusStrip mailboxes={mailboxes} />
	                  <div className="sectionTitle">
	                    <h3>邮箱注册 Job</h3>
	                    <button className="secondaryButton" onClick={() => openView('jobs')}>
	                      <ListChecks size={14} /> 全部工作流
	                    </button>
	                  </div>
	                  <JobTable jobs={mailboxRegisterJobs.slice(0, 20)} selected={selectedJob?.job_id} busy={busy} onSelect={selectJob} onRetry={retryJob} />
	                </div>
	              </div>
	            </section>
	          )}

	          {activeView === 'jobs' && (
            <section className="workspace jobsWorkspace">
              <div className="panel jobsPanel">
                <PanelHeader title="工作流" icon={<Activity size={16} />}>
                  <select value={jobStatus} onChange={(e) => setJobStatus(e.target.value)}>
                    {jobStatusOptions.map((s) => <option key={s} value={s}>{s || '全部状态'}</option>)}
                  </select>
                </PanelHeader>
                <JobTable jobs={jobs} selected={selectedJob?.job_id} busy={busy} onSelect={selectJob} onRetry={retryJob} />
              </div>
            </section>
          )}
        </div>
      </section>

      <DetailDrawer open={!!selectedAccount} title="账号详情" onClose={closeDetails}>
        {selectedAccount && (
          <AccountDetails
            account={selectedAccount}
            showSecrets={showSecrets}
            busy={busy}
            onSessionSave={(account, sessionToken) => updateAccountAuth(account, { session_token: sessionToken })}
            onAccessSave={(account, accessToken) => updateAccountAuth(account, { access_token: accessToken })}
            onProbePlusTrial={(account) => runAccountWorkflow('资格探测', '/api/workflows/probe-plus-trial', account)}
            onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
            onRefreshAccessToken={refreshAccountAccessToken}
            refreshingAccessToken={refreshingAccessTokenIds.has(selectedAccount.account_id)}
          />
        )}
      </DetailDrawer>

      <DetailDrawer open={!!selectedJob} title="工作流详情" onClose={closeDetails}>
        {selectedJob && (
          <JobDetails
            job={selectedJob}
            busy={busy}
            onJobRetry={retryJob}
            onOtpSubmit={submitJobOtp}
          />
        )}
      </DetailDrawer>

      <DetailDrawer open={!!selectedMailbox} title="邮箱详情" onClose={closeDetails}>
        {selectedMailbox && (
          <MailboxDetails mailbox={selectedMailbox} showSecrets={showSecrets} />
        )}
      </DetailDrawer>
    </main>
  );
}

function NavItem({ active, icon, label, count, onClick }: {
  active: boolean;
  icon: React.ReactNode;
  label: string;
  count: number;
  onClick: () => void;
}) {
  return (
    <button className={`navItem ${active ? 'active' : ''}`} onClick={onClick}>
      <span>{icon}</span>
      <strong>{label}</strong>
      <em>{count}</em>
    </button>
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
          <button className="iconButton" {...buttonHint('关闭')} onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        {children}
      </aside>
    </div>
  );
}

function AccountDetails({ account, showSecrets, busy, refreshingAccessToken, onSessionSave, onAccessSave, onProbePlusTrial, onLogin, onRefreshAccessToken }: {
  account: Account;
  showSecrets: boolean;
  busy: boolean;
  refreshingAccessToken: boolean;
  onSessionSave: (account: Account, sessionToken: string) => Promise<void>;
  onAccessSave: (account: Account, accessToken: string) => Promise<void>;
  onProbePlusTrial: (account: Account) => void;
  onLogin: (account: Account) => void;
  onRefreshAccessToken: (account: Account) => Promise<void>;
}) {
  return (
    <div className="details">
      <section>
        <div className="sectionTitle">
          <h3>账号</h3>
          <div className="sectionActions">
            {canRefreshAccessToken(account) && (
              <button {...buttonHint('使用当前 Session 自动获取 Access Token')} disabled={busy || refreshingAccessToken} onClick={() => void onRefreshAccessToken(account)}>
                <KeyRound size={14} /> {refreshingAccessToken ? '获取中' : '自动获取 Access Token'}
              </button>
            )}
            {canLoginSession(account) && (
              <button {...buttonHint(loginActionHint(account))} disabled={busy} onClick={() => onLogin(account)}>
                <KeyRound size={14} /> {loginActionLabel(account)}
              </button>
            )}
            <button {...buttonHint('探测当前账号是否可用 Plus 试用')} disabled={busy || !canProbePlusTrial(account)} onClick={() => onProbePlusTrial(account)}>
              <Search size={14} /> 探测资格
            </button>
          </div>
        </div>
        <KV label="ID" value={account.account_id} mono />
        <KV label="Status" value={account.status || '-'} />
        <KV label="试用资格" value={trialText(account.plus_trial_eligible)} />
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

function JobDetails({ job, busy, onJobRetry, onOtpSubmit }: {
  job: Job;
  busy: boolean;
  onJobRetry: (job: Job) => void;
  onOtpSubmit: (job: Job, otp: string) => Promise<void>;
}) {
  return (
    <div className="details">
      <section>
        <div className="sectionTitle">
          <h3>工作流</h3>
          {canRetryJob(job) && (
            <button disabled={busy} onClick={() => onJobRetry(job)}>
              <RefreshCcw size={14} /> 重试
            </button>
          )}
        </div>
        <KV label="Job" value={job.job_id} mono />
        <KV label="Action" value={job.action} />
        <KV label="Status" value={job.status} />
        <KV label="Error" value={job.error_message || '-'} />
        {canSubmitOtp(job) && <OtpSubmitter job={job} onSubmit={onOtpSubmit} />}
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

function OtpSubmitter({ job, onSubmit }: {
  job: Job;
  onSubmit: (job: Job, otp: string) => Promise<void>;
}) {
  const [otp, setOtp] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const label = otpSubmitLabel(job);

  async function submit() {
    const value = otp.trim();
    if (!value) return;
    setSubmitting(true);
    try {
      await onSubmit(job, value);
      setOtp('');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="otpSubmitter">
      <span><KeyRound size={14} /> {label}</span>
      <div>
        <input
          inputMode="numeric"
          autoComplete="one-time-code"
          placeholder="验证码"
          value={otp}
          onChange={(event) => setOtp(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <button className="primaryButton" disabled={submitting || !otp.trim()} onClick={() => void submit()}>
          <KeyRound size={14} /> 提交
        </button>
      </div>
    </div>
  );
}

function AccountTable({ accounts, selected, showSecrets, runningAccountIds, refreshingAccessTokenIds, busy, onSelect, onRegister, onLogin, onActivate, onProbePlusTrial, onRegisterActivate, onRefreshAccessToken, onDelete }: {
  accounts: Account[];
  selected?: string;
  showSecrets: boolean;
  runningAccountIds: Set<string>;
  refreshingAccessTokenIds: Set<string>;
  busy: boolean;
  onSelect: (a: Account) => void;
  onRegister: (a: Account) => void;
  onLogin: (a: Account) => void;
  onActivate: (a: Account) => void;
  onProbePlusTrial: (a: Account) => void;
  onRegisterActivate: (a: Account) => void;
  onRefreshAccessToken: (a: Account) => Promise<void>;
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
            <th>试用</th>
            <th>Session</th>
            <th>Access</th>
            <th>更新</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {accounts.map((account) => {
            const accountBusy = runningAccountIds.has(account.account_id);
            const refreshingAccessToken = refreshingAccessTokenIds.has(account.account_id);
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
                <td><TrialBadge eligible={account.plus_trial_eligible} /></td>
                <td className="mono">{showSecrets ? short(account.session_token, 18) : mask(account.session_token)}</td>
                <td className="mono">{showSecrets ? short(account.access_token, 18) : mask(account.access_token)}</td>
                <td>{formatUnix(account.updated_at)}</td>
                <td>
                  <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                    {accountBusy ? (
                      <span className="busyLabel">进行中</span>
                    ) : (
                      <>
                        {canRegister(account) && <button {...buttonHint('注册 OpenAI 账号')} disabled={busy} onClick={() => onRegister(account)}><Play size={14} /></button>}
                        {canLoginSession(account) && <button {...buttonHint(loginActionHint(account))} disabled={busy} onClick={() => onLogin(account)}><KeyRound size={14} /></button>}
                        {canRefreshAccessToken(account) && (
                          <button
                            {...buttonHint('使用当前 Session 自动获取 Access Token')}
                            disabled={busy || refreshingAccessToken}
                            onClick={() => void onRefreshAccessToken(account)}
                          >
                            <KeyRound size={14} />
                          </button>
                        )}
                        {canActivate(account) && <button {...buttonHint('激活订阅支付流程')} disabled={busy} onClick={() => onActivate(account)}><Zap size={14} /></button>}
                        {canProbePlusTrial(account) && <button {...buttonHint('探测 Plus 试用资格')} disabled={busy} onClick={() => onProbePlusTrial(account)}><Search size={14} /></button>}
                        {canRegister(account) && <button {...buttonHint('注册并激活账号')} disabled={busy} onClick={() => onRegisterActivate(account)}><ShieldCheck size={14} /></button>}
                        <button className="dangerButton" {...buttonHint('删除账号')} disabled={busy} onClick={() => onDelete(account)}><Trash2 size={14} /></button>
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
                  {canRetryJob(job) ? (
          <button {...buttonHint('按同参数重试')} disabled={busy} onClick={() => onRetry(job)}>
            <RefreshCcw size={14} />
          </button>
                  ) : (
                    <span className="muted">-</span>
                  )}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function MailboxPanel({ mailboxes, selected, busy, showSecrets, oauthing, onSelect, onOAuth, onDone, onError }: {
  mailboxes: Mailbox[];
  selected?: string;
  busy: boolean;
  showSecrets: boolean;
  oauthing: string;
  onSelect: (mailbox: Mailbox) => void;
  onOAuth: (emailAddress?: string) => Promise<void>;
  onDone: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [form, setForm] = useState({ email: '', password: '', refresh_token: '', access_token: '', status: 'AVAILABLE' });
  const [working, setWorking] = useState(false);

  function update(key: keyof typeof form, value: string) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function saveMailbox() {
    setWorking(true);
    try {
      const payload = { ...form, status: form.status || 'AVAILABLE' };
      const resp = await api<Mailbox>('/api/mailboxes', { method: 'POST', body: JSON.stringify(payload) });
      setForm({ email: '', password: '', refresh_token: '', access_token: '', status: 'AVAILABLE' });
      onDone(`邮箱已入池: ${resp.email_address}`);
    } catch (err) {
      onError(errorText(err));
    } finally {
      setWorking(false);
    }
  }

  return (
    <>
      <MailboxStatusStrip mailboxes={mailboxes} />
      <div className="mailboxForm">
        <input placeholder="邮箱" value={form.email} onChange={(e) => update('email', e.target.value)} />
        <input placeholder="邮箱密码，可空" type="password" value={form.password} onChange={(e) => update('password', e.target.value)} />
        <input placeholder="Refresh token，可空" type="password" value={form.refresh_token} onChange={(e) => update('refresh_token', e.target.value)} />
        <input placeholder="Access token，可空" type="password" value={form.access_token} onChange={(e) => update('access_token', e.target.value)} />
        <select value={form.status} onChange={(e) => update('status', e.target.value)}>
          {mailboxStatusOptions.filter(Boolean).map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <button onClick={saveMailbox} disabled={busy || working || !form.email.trim()}><Plus size={15} /> 入池</button>
      </div>
      <div className="tableWrap">
        <table>
          <thead>
            <tr><th>邮箱</th><th>类型</th><th>状态</th><th>Token</th><th>更新</th><th>错误</th><th>操作</th></tr>
          </thead>
          <tbody>
            {mailboxes.map((mailbox) => {
              const isOAuthing = oauthing === mailbox.email_address || oauthing === '*';
              const canOAuth = mailbox.is_primary && !!mailbox.password;
              return (
                <tr key={mailbox.email_address} className={selected === mailbox.email_address ? 'selected' : ''} onClick={() => onSelect(mailbox)}>
                  <td>
                    <div className="cellStack">
                      <span>{showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}</span>
                      <small>{mailbox.primary_email || '-'}</small>
                    </div>
                  </td>
                  <td>{mailbox.is_primary ? '主邮箱' : 'Alias'}</td>
                  <td><StatusBadge status={mailbox.status} /></td>
                  <td><TokenBadge mailbox={mailbox} /></td>
                  <td>{formatUnix(mailbox.updated_at)}</td>
                  <td title={mailbox.last_error}>{compactToast(mailbox.last_error || '-')}</td>
                  <td>
                    <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                      {canOAuth ? (
                        <button title="启动 OAuth 流程" disabled={busy || !!oauthing} onClick={() => onOAuth(mailbox.email_address)}>
                          <KeyRound size={14} /> {isOAuthing ? '提交中' : 'OAuth'}
                        </button>
                      ) : (
                        <span className="muted">-</span>
                      )}
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

function MailboxOAuthTable({ mailboxes, busy, showSecrets, oauthing, onOAuth }: {
  mailboxes: Mailbox[];
  busy: boolean;
  showSecrets: boolean;
  oauthing: string;
  onOAuth: (emailAddress?: string) => Promise<void>;
}) {
  return (
    <div className="tableWrap oauthTableWrap">
      <table>
        <thead>
          <tr><th>邮箱</th><th>状态</th><th>Token</th><th>更新</th><th>操作</th></tr>
        </thead>
        <tbody>
          {mailboxes.map((mailbox) => {
            const isOAuthing = oauthing === mailbox.email_address || oauthing === '*';
            return (
              <tr key={mailbox.email_address}>
                <td>
                  <div className="cellStack">
                    <span>{showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}</span>
                    <small>{mailbox.refresh_token ? '已授权' : '缺 OAuth'}</small>
                  </div>
                </td>
                <td><StatusBadge status={mailbox.status} /></td>
                <td><TokenBadge mailbox={mailbox} /></td>
                <td>{formatUnix(mailbox.updated_at)}</td>
                <td>
                  <button
                    className="rowButton"
                    title="执行 Microsoft OAuth"
                    disabled={busy || !!oauthing}
                    onClick={() => onOAuth(mailbox.email_address)}
                  >
                    <KeyRound size={14} /> {isOAuthing ? '处理中' : 'OAuth'}
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function MailboxStatusStrip({ mailboxes }: { mailboxes: Mailbox[] }) {
  const items = [
    ['AVAILABLE', '可用'],
    ['ASSIGNED', '已分配'],
    ['REGISTERED', '已注册'],
    ['OAUTH_PENDING', '待 OAuth'],
    ['AUTH_FAILED', '认证失败'],
    ['BLOCKED', '已封禁']
  ];
  return (
    <div className="mailboxStatusStrip">
      {items.map(([status, label]) => (
        <div key={status}>
          <strong>{mailboxes.filter((mailbox) => mailbox.status === status).length}</strong>
          <span>{label}</span>
        </div>
      ))}
    </div>
  );
}

function MailboxDetails({ mailbox, showSecrets }: {
  mailbox: Mailbox;
  showSecrets: boolean;
}) {
  return (
    <div className="details">
      <section>
        <h3>邮箱</h3>
        <KV label="Email" value={showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)} />
        <KV label="Password" value={showSecrets ? mailbox.password : mask(mailbox.password)} mono />
        <KV label="Status" value={mailbox.status || '-'} />
        <KV label="Type" value={mailbox.is_primary ? '主邮箱' : 'Alias'} />
        <KV label="Primary" value={mailbox.primary_email || '-'} />
        <KV label="Refresh" value={showSecrets ? mailbox.refresh_token : mask(mailbox.refresh_token)} mono />
        <KV label="Access" value={showSecrets ? mailbox.access_token : mask(mailbox.access_token)} mono />
        <KV label="Created" value={formatUnix(mailbox.created_at)} />
        <KV label="Updated" value={formatUnix(mailbox.updated_at)} />
        <KV label="Error" value={mailbox.last_error || '-'} />
      </section>
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
      <button className="copyButton" {...buttonHint(`复制 ${label}`)} disabled={!value.trim()} onClick={() => copyText(value)}>
        <Copy size={14} />
      </button>
      <button {...buttonHint(`保存 ${label}`)} onClick={save} disabled={saving || value.trim() === current}>
        <Save size={14} /> 保存
      </button>
    </div>
  );
}

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="kv">
      <span>{label}</span>
      <button className={mono ? 'mono valueButton' : 'valueButton'} {...buttonHint(`复制 ${label}`)} onClick={() => copyText(value)}>{value || '-'}</button>
      <button className="copyButton" {...buttonHint(`复制 ${label}`)} disabled={!value} onClick={() => copyText(value)}>
        <Copy size={14} />
      </button>
    </div>
  );
}

function StatusBadge({ status, retryable }: { status: string; retryable?: boolean }) {
  const cls = status.includes('FAILED') || status.includes('EXISTS') || status === 'BLOCKED' ? 'bad' : status === 'SUCCEEDED' || status === 'ACTIVATED' || status === 'REGISTERED' ? 'good' : 'mid';
  return <span className={`badge ${cls}`}>{status || '-'}{retryable ? ' / retry' : ''}</span>;
}

function TrialBadge({ eligible }: { eligible?: boolean }) {
  if (eligible === true) return <span className="badge good">0元</span>;
  if (eligible === false) return <span className="badge bad">非0元</span>;
  return <span className="badge mid">未知</span>;
}

function TokenBadge({ mailbox }: { mailbox: Mailbox }) {
  if (mailbox.refresh_token) return <span className="badge good">Refresh</span>;
  if (mailbox.access_token) return <span className="badge mid">Access</span>;
  return <span className="badge bad">None</span>;
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data as T;
}

function canRegister(account: Account) {
  return account.status !== 'EMAIL_ALREADY_EXISTS' && !hasRegisteredSession(account);
}

function canActivate(account: Account) {
  return account.status !== 'ACTIVATED' && (!!account.session_token || !!account.access_token);
}

function canProbePlusTrial(account: Account) {
  return account.status !== 'ACTIVATED' && !!account.session_token;
}

function canRefreshAccessToken(account: Account) {
  return !!account.session_token && !account.access_token;
}

function canLoginSession(account: Account) {
  return !!account.email && !!account.password;
}

function loginActionLabel(account: Account) {
  if (!account.session_token) return '登录获取 Session';
  if (!account.access_token) return '登录刷新 Access Token';
  return '登录刷新 Token';
}

function loginActionHint(account: Account) {
  if (!account.session_token) return '通过账号密码登录并获取 Session Token';
  if (!account.access_token) return '重新登录并刷新 Access Token';
  return '重新登录并刷新 Session / Access Token';
}

function buttonHint(label: string) {
  return { title: label, 'aria-label': label, 'data-tooltip': label };
}

function hasRegisteredSession(account: Account) {
  return account.status === 'REGISTERED' || account.status === 'ACTIVATED' || !!account.session_token || !!account.access_token;
}

function canRetryJob(job: Job) {
  return job.retryable && job.status.startsWith('FAILED');
}

function canSubmitOtp(job: Job) {
  return job.status === 'RUNNING' && (job.action === 'REGISTER' || job.action === 'LOGIN_SESSION' || job.action === 'ACTIVATE' || job.action === 'REGISTER_AND_ACTIVATE');
}

function otpSubmitLabel(job: Job) {
  if (job.action === 'LOGIN_SESSION') return '登录 OTP';
  if (job.action === 'ACTIVATE' || (job.action === 'REGISTER_AND_ACTIVATE' && job.last_step === 'gopay_payment')) {
    return '支付 OTP';
  }
  return '注册 OTP';
}

function short(value: string, size = 8) {
  return value ? `${value.slice(0, size)}…` : '-';
}

function mask(value: string) {
  return value ? '••••••••' : '-';
}

function maskEmail(value: string) {
  if (!value) return '-';
  const [local, domain] = value.split('@');
  if (!local || !domain) return mask(value);
  return `${local.slice(0, 2)}***@${domain}`;
}

function formatUnix(value: number) {
  return value ? new Date(value * 1000).toLocaleString() : '-';
}

function trialText(value?: boolean) {
  if (value === true) return '0元试用';
  if (value === false) return '非0元';
  return '未知';
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

function copyText(value: string) {
  if (!value) return;
  void navigator.clipboard?.writeText(value);
}

createRoot(document.getElementById('root')!).render(<App />);

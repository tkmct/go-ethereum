import React, { useState } from 'react';
import { FiSearch } from 'react-icons/fi';
import { BrowserRouter, Link, NavLink, Route, Routes } from 'react-router-dom';
import Compare from './routes/Compare';
import ProofMpt from './routes/ProofMpt';
import ProofUbt from './routes/ProofUbt';
import Witness from './routes/Witness';
import AccountRangeModal from './components/AccountRangeModal';

const navItems = [
  { to: '/compare', label: 'Compare' },
  { to: '/proofs/mpt', label: 'MPT Proof' },
  { to: '/proofs/ubt', label: 'UBT Proof' },
  { to: '/witness', label: 'Witness' },
];

export default function App() {
  const [isRangeOpen, setIsRangeOpen] = useState(false);

  return (
    <BrowserRouter>
      <div className="app">
        <header className="app-header">
          <div className="brand">
            <Link to="/compare">UBT Checker</Link>
            <span>State + Proof Inspector</span>
          </div>
          <div className="header-actions">
            <nav className="app-nav">
              {navItems.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    isActive ? 'nav-link active' : 'nav-link'
                  }
                >
                  {item.label}
                </NavLink>
              ))}
            </nav>
            <button type="button" className="secondary icon-button" onClick={() => setIsRangeOpen(true)}>
              <FiSearch aria-hidden="true" />
              Fetch Addresses
            </button>
          </div>
        </header>
        <main className="app-main">
          <Routes>
            <Route path="/" element={<Compare />} />
            <Route path="/compare" element={<Compare />} />
            <Route path="/proofs/mpt" element={<ProofMpt />} />
            <Route path="/proofs/ubt" element={<ProofUbt />} />
            <Route path="/witness" element={<Witness />} />
          </Routes>
        </main>
        <AccountRangeModal isOpen={isRangeOpen} onClose={() => setIsRangeOpen(false)} />
      </div>
    </BrowserRouter>
  );
}

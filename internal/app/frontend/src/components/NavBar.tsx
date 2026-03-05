import { NavLink } from 'react-router-dom';

const navStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '0',
  padding: '0 16px',
  height: '48px',
  background: '#181825',
  borderBottom: '1px solid #45475a',
  flexShrink: 0,
};

const logoStyle: React.CSSProperties = {
  fontWeight: 700,
  fontSize: '16px',
  color: '#89b4fa',
  marginRight: '24px',
  letterSpacing: '-0.5px',
};

const linkBase: React.CSSProperties = {
  padding: '8px 16px',
  fontSize: '14px',
  textDecoration: 'none',
  color: '#a6adc8',
  borderBottom: '2px solid transparent',
  transition: 'color 0.15s, border-color 0.15s',
};

export default function NavBar() {
  return (
    <nav style={navStyle}>
      <span style={logoStyle}>CodeEagle</span>
      <NavLink
        to="/search"
        style={({ isActive }) => ({
          ...linkBase,
          color: isActive ? '#cdd6f4' : '#a6adc8',
          borderBottomColor: isActive ? '#89b4fa' : 'transparent',
        })}
      >
        Search
      </NavLink>
      <NavLink
        to="/ask"
        style={({ isActive }) => ({
          ...linkBase,
          color: isActive ? '#cdd6f4' : '#a6adc8',
          borderBottomColor: isActive ? '#89b4fa' : 'transparent',
        })}
      >
        Ask
      </NavLink>
    </nav>
  );
}

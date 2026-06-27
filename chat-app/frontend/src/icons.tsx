import React from 'react';

type Props = { style?: React.CSSProperties; size?: number };

const base = (size?: number) => ({
  width: size ?? 16,
  height: size ?? 16,
  viewBox: '0 0 1024 1024',
  fill: 'currentColor',
  xmlns: 'http://www.w3.org/2000/svg',
});

export const EditOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M832 512l-320 320-64-64 256-256-256-256 64-64z" />
  </svg>
);

export const PlusOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M482 152h60v270h270v60H542v270h-60V482H212v-60h270z" />
  </svg>
);

export const MessageOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M128 192h768a64 64 0 0 1 64 64v448a64 64 0 0 1-64 64H304l-160 128V256a64 64 0 0 1 64-64zm64 128v320l96-64h608V320H192z" />
  </svg>
);

export const SettingOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 256a256 256 0 1 0 0 512 256 256 0 0 0 0-512zm0 448a192 192 0 1 1 0-384 192 192 0 0 1 0 384zm320-256a32 32 0 0 1-32 32h-64v-64h64a32 32 0 0 1 32 32zm-704 0a32 32 0 0 1 32 32h64v-64H160a32 32 0 0 0-32 32zm573-205l45-45-45-45-45 45 45 45zm-378 410l45-45-45-45-45 45 45 45zm0-410l-45-45-45 45 45 45 45-45zm378 410l-45-45-45 45 45 45 45-45z" />
  </svg>
);

export const UserOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 512a160 160 0 1 0 0-320 160 160 0 0 0 0 320zm0 64c-138 0-256 70-256 160v64h512v-64c0-90-118-160-256-160z" />
  </svg>
);

export const FolderOpenOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M192 192h224l64 64h352a64 64 0 0 1 64 64v416a64 64 0 0 1-64 64H192a64 64 0 0 1-64-64V256a64 64 0 0 1 64-64zm32 192v288h576V384H224z" />
  </svg>
);

export const SendOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M128 192l768 320-768 320 96-320z" />
  </svg>
);

export const StopOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M192 192h640v640H192z" />
  </svg>
);

export const CloseOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 458l256 256-54 54-256-256-256 256-54-54 256-256-256-256 54-54 256 256 256-256 54 54z" />
  </svg>
);

export const RefreshOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 192a320 320 0 1 0 286 480l-58-34a256 256 0 1 1 0-292l58-34A320 320 0 0 0 512 192zm256-128v192h-192l96-96z" />
  </svg>
);

export const ChevronDownOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M192 384l320 320 320-320z" />
  </svg>
);

export const ChevronUpOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M192 640l320-320 320 320z" />
  </svg>
);

export const Bot: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size ?? 28)} style={style} aria-hidden>
    <path d="M192 448h640v320a64 64 0 0 1-64 64H256a64 64 0 0 1-64-64V448zm-32-128a32 32 0 0 1 32-32h640a32 32 0 0 1 32 32v96H160v-96zM384 256V128h256v128h-64v-64h-128v64h-64zm128 384a48 48 0 1 0 0 96 48 48 0 0 0 0-96z" />
  </svg>
);

export const FolderIcon: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M896 256H512l-64-64H128a64 64 0 0 0-64 64v512a64 64 0 0 0 64 64h768a64 64 0 0 0 64-64V320a64 64 0 0 0-64-64z" />
  </svg>
);

export const CheckOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M384 768l-192-192 64-64 128 128 320-320 64 64z" />
  </svg>
);

export const EyeOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 256c-205 0-368 144-448 256 80 112 243 256 448 256s368-144 448-256c-80-112-243-256-448-256zm0 448a192 192 0 1 1 0-384 192 192 0 0 1 0 384z" />
  </svg>
);

export const EyeInvisibleOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M512 256c-205 0-368 144-448 256 80 112 243 256 448 256s368-144 448-256c-80-112-243-256-448-256zm0 64a192 192 0 1 1 0 384 192 192 0 0 1 0-384z" />
  </svg>
);

export const CodeOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M256 384l-160 128 160 128 45-45-115-83 115-83zM768 384l-45 45 115 83-115 83 45 45 160-128zM608 256l-192 512 60 22 192-512z" />
  </svg>
);

export const SaveOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M192 128h512l192 192v480a64 64 0 0 1-64 64H192a64 64 0 0 1-64-64V192a64 64 0 0 1 64-64zm64 64v128h384V192H256zm64 384h256v-64H320v64z" />
  </svg>
);

export const WrenchOutlined: React.FC<Props> = ({ size, style }) => (
  <svg {...base(size)} style={style} aria-hidden>
    <path d="M704 256a192 192 0 0 0-256 256L192 768l64 64 256-256a192 192 0 0 0 192-192 192 192 0 0 0-32-112l-128 128-64-64 128-128A192 192 0 0 0 704 256z" />
  </svg>
);
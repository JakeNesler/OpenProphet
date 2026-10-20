import dotenv from 'dotenv';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.dirname(fileURLToPath(import.meta.url));
// MCP clients may launch us from another project. Load our config before modules that
// read environment variables, and keep dotenv output off the stdio protocol channel.
dotenv.config({ path: process.env.DOTENV_CONFIG_PATH || path.join(root, '.env'), quiet: true });

export const dataDir = process.env.OPENPROPHET_DATA_DIR || path.join(root, 'data');

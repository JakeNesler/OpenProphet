import { GoogleGenAI } from '@google/genai';

export async function generateNewsSummary(prompt, {
  apiKey = process.env.GEMINI_API_KEY,
  model = process.env.GEMINI_MODEL || 'gemini-3.5-flash-lite',
  client,
} = {}) {
  if (!client && !apiKey) {
    throw new Error('GEMINI_API_KEY is required for news summarization');
  }
  const genAI = client || new GoogleGenAI({ apiKey });
  const result = await genAI.models.generateContent({ model, contents: prompt });
  if (!result.text?.trim()) {
    throw new Error('Gemini returned no text for the news summary');
  }
  return result.text;
}

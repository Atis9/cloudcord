// Pipe directly to kubectl; never write credentials into the working tree.
const names = ['DISCORD_TOKEN', 'DISCORD_APPLICATION_ID', 'DISCORD_GUILD_ID', 'CLOUDFLARE_API_BASE_URL', 'CLOUDFLARE_BOT_API_TOKEN'];
const data = {};
for (const name of names) {
  const value = process.env[name];
  if (!value) throw new Error(`Missing ${name}`);
  data[name] = Buffer.from(value).toString('base64');
}
process.stdout.write(JSON.stringify({apiVersion:'v1', kind:'Secret', metadata:{name:'cloudcord-secret',namespace:'nyantech'}, type:'Opaque', data}));

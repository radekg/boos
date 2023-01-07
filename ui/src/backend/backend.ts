import axios, { AxiosResponse } from 'axios';

export interface BackEndData {
    address: string,
    iceServers: string[],
}

export const loadBackendData = (): Promise<AxiosResponse<BackEndData, any>> => {
   return axios.get<BackEndData>("/backend")
}

export const getWebsocketAddress = (backEndData: BackEndData): string | null => {
    const address = new URL(backEndData.address)
    if (address.protocol === "https:") {
        return `wss://${address.host}/ws`
    } else if (address.protocol === "http:") {
        return `ws://${address.host}/ws`
    }
    return null
}